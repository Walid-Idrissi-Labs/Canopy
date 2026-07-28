//go:build unix

package exec

// Where the signals actually go.
//
// The behavioural tests elsewhere in this package say that a cancelled or timed out command takes
// its children with it. These say which group it takes them from, which is the part that has no
// symptom when it is wrong: a signal sent to a group id that has gone back into circulation does not
// fail, it lands on somebody else's job.

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// never is a channel that no clock will ever fire, so a test can pin the escalation to exactly one
// of its two branches.
func never() <-chan time.Time { return nil }

// closed is a reaped signal that has already happened.
func closed() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// recorder collects what would have been signalled, and answers the group probe however the test
// needs it answered.
type recorder struct {
	calls      []signalCall
	groupThere bool
}

type signalCall struct {
	pid int
	sig syscall.Signal
}

func (r *recorder) signal(pid int, sig syscall.Signal) error {
	r.calls = append(r.calls, signalCall{pid: pid, sig: sig})
	if sig == 0 && !r.groupThere {
		return syscall.ESRCH
	}
	return nil
}

func (r *recorder) sent(sig syscall.Signal) bool {
	for _, call := range r.calls {
		if call.sig == sig {
			return true
		}
	}
	return false
}

// The one that has no symptom until it has a very bad one. A process group's id is its leader's pid,
// and the kernel is free to hand that number out again the moment the group empties. Sending SIGKILL
// to it a quarter of a second later, without checking, is a signal aimed at whatever holds the
// number by then.
func TestTheSecondSignalIsNotSentToAProcessGroupThatHasAlreadyGone(t *testing.T) {
	r := &recorder{groupThere: false}

	escalate(4242, closed(), never(), r.signal)

	if r.sent(syscall.SIGKILL) {
		t.Error("SIGKILL was sent to a group with nothing left in it, which is an id the kernel " +
			"may already have given to somebody else")
	}
	if len(r.calls) != 1 || r.calls[0].sig != 0 {
		t.Errorf("calls = %+v, want exactly one probe and nothing else", r.calls)
	}
}

// The other half of the same rule. A child that outlived its own parent is the orphan this whole
// mechanism exists for, and declining to signal it would trade one bug for a worse one.
func TestTheSecondSignalStillReachesChildrenThatOutlivedTheirLeader(t *testing.T) {
	r := &recorder{groupThere: true}

	escalate(4242, closed(), never(), r.signal)

	if !r.sent(syscall.SIGKILL) {
		t.Fatalf("a group that still has members was left alone: %+v", r.calls)
	}
	for _, call := range r.calls {
		if call.pid != -4242 {
			t.Errorf("signal %v went to pid %d, want the group -4242: a positive pid addresses one "+
				"process and leaves everything it started running", call.sig, call.pid)
		}
	}
}

// While the leader is unreaped its id cannot have been reused, so the escalation needs no permission
// and asks for none. Worth pinning down because it is the ordinary path: this is what stops a
// command that ignores SIGTERM from running forever.
func TestTheSecondSignalFollowsTheGracePeriodWhileTheCommandIsStillRunning(t *testing.T) {
	r := &recorder{}
	grace := make(chan time.Time, 1)
	grace <- time.Now()

	escalate(4242, make(chan struct{}), grace, r.signal)

	if len(r.calls) != 1 {
		t.Fatalf("calls = %+v, want one signal and no probe: there is nothing to check while the "+
			"leader is still there", r.calls)
	}
	if r.calls[0].sig != syscall.SIGKILL || r.calls[0].pid != -4242 {
		t.Errorf("call = %+v, want SIGKILL to the group -4242", r.calls[0])
	}
}

// The same rule applies to the first signal, and it is asked separately rather than once for both.
// A kill is two signals a quarter of a second apart, and an answer cached across that gap would be
// exactly as stale as no answer at all.
func TestWhetherAGroupIsOursToSignalIsAskedOfTheGroupAndNotAssumed(t *testing.T) {
	cases := []struct {
		name   string
		reaped <-chan struct{}
		there  bool
		want   bool
		probes int
	}{
		{"still running", make(chan struct{}), false, true, 0},
		{"reaped, children left behind", closed(), true, true, 1},
		{"reaped, nothing left", closed(), false, false, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{groupThere: tc.there}

			if got := oursToSignal(4242, tc.reaped, r.signal); got != tc.want {
				t.Errorf("oursToSignal = %v, want %v", got, tc.want)
			}
			if len(r.calls) != tc.probes {
				t.Errorf("%d calls, want %d: asking costs a syscall and not asking costs "+
					"somebody else's processes", len(r.calls), tc.probes)
			}
		})
	}
}

// The group being its own is the whole mechanism. Without Setpgid the command shares Canopy's group,
// and everything above becomes either a no-op or an attempt to kill Canopy.
func TestEachCommandRunsInAProcessGroupOfItsOwn(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not available here")
	}

	// The shell's own pid, then the group it is in. If the group was set they are the same number,
	// because a new group is named after the process that leads it.
	result := run(t, context.Background(), "echo $$; ps -o pgid= -p $$", Options{})
	if !result.Succeeded() {
		t.Fatalf("result = %+v", result)
	}

	fields := strings.Fields(result.Output)
	if len(fields) != 2 {
		t.Fatalf("output = %q, want the shell's pid and its group", result.Output)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("reading the pid from %q: %v", result.Output, err)
	}
	pgid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("reading the group from %q: %v", result.Output, err)
	}

	if pgid != pid {
		t.Errorf("the command runs in group %d rather than leading its own group %d, so killing "+
			"the group would either miss it or reach further than intended", pgid, pid)
	}

	ours, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid == ours {
		t.Errorf("the command shares Canopy's own process group %d, so a group kill aimed at the "+
			"command would take Canopy down with it", ours)
	}
}
