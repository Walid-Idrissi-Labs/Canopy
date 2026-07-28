//go:build unix

package exec

// Where the signals actually go.
//
// The behavioural tests elsewhere in this package say that a cancelled or timed out command takes its
// children with it. These say which group it takes them from, which is the part that has no symptom
// when it is wrong: a signal sent to a group id that has gone back into circulation does not fail, it
// lands on somebody else's job.

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// never is a clock that will not fire, so a test can pin the escalation to its first signal only.
func never() <-chan time.Time { return nil }

// now is a grace period that is already over.
func now() <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

// recorder collects what would have been signalled, without anything being signalled for real.
type recorder struct {
	mu    sync.Mutex
	calls []signalCall
}

type signalCall struct {
	pid int
	sig syscall.Signal
}

func (r *recorder) signal(pid int, sig syscall.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, signalCall{pid: pid, sig: sig})
	return nil
}

func (r *recorder) sent(sig syscall.Signal) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if call.sig == sig {
			return true
		}
	}
	return false
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// sleeper starts a real process in its own group, so the pid and the reap are real rather than
// simulated. Nothing in these tests signals it for real; the recorder stands in for the syscall.
func sleeper(t *testing.T) *Child {
	t.Helper()

	cmd := exec.Command("sleep", "30")
	Contain(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a test process: %v", err)
	}
	child := Started(cmd)

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = child.Wait()
	})
	return child
}

// The rule the whole type exists for. A process group's id is its leader's pid, and the kernel is
// free to hand that number out again once the leader has been waited on. Signalling it after that
// point is a signal aimed at whatever holds the number by then.
func TestNoSignalIsSentToAGroupWhoseLeaderHasBeenReaped(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	Contain(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start a test process: %v", err)
	}
	_ = cmd.Process.Kill()

	child := Started(cmd)
	if err := child.Wait(); err == nil {
		t.Log("the killed process was waited on cleanly, which is fine for this test")
	}

	r := &recorder{}
	child.stopWith(r.signal, now())

	// The first signal is sent before stopWith returns, so this half is exact.
	if r.count() != 0 {
		t.Fatalf("calls = %+v, want none: the leader has been reaped, so its pid may name somebody "+
			"else's process group by now and there is nothing here that is safe to signal", r.calls)
	}

	// The escalation runs on its own and there is no handle to join on, so proving a negative here
	// means giving it a window in which to have got it wrong. A hundred milliseconds against a grace
	// period that has already expired is several orders of magnitude of headroom.
	time.Sleep(100 * time.Millisecond)

	if r.count() != 0 {
		t.Errorf("calls = %+v, want none: the escalation signalled a group whose leader had already "+
			"been waited on", r.calls)
	}
}

// The ordinary path, and the reason any of this exists: a command that is still running gets its
// group terminated, so the workers it started go with it.
func TestTheFirstSignalGoesToTheWholeGroupWhileTheLeaderIsStillThere(t *testing.T) {
	child := sleeper(t)
	r := &recorder{}

	child.stopWith(r.signal, never())

	if r.count() != 1 {
		t.Fatalf("calls = %+v, want exactly one", r.calls)
	}
	call := r.calls[0]
	if call.sig != syscall.SIGTERM {
		t.Errorf("first signal is %v, want SIGTERM: a process with cleanup to do gets the chance", call.sig)
	}
	if call.pid != -child.cmd.Process.Pid {
		t.Errorf("signal went to pid %d, want the group %d: a positive pid addresses one process and "+
			"leaves everything it started running", call.pid, -child.cmd.Process.Pid)
	}
}

// A process that ignores SIGTERM is exactly what the second signal is for. While the leader is
// unreaped its id cannot have been reused, so this needs no permission and asks for none.
func TestTheSecondSignalFollowsTheGracePeriodWhileTheCommandIsStillRunning(t *testing.T) {
	child := sleeper(t)
	r := &recorder{}

	child.stopWith(r.signal, now())

	waitFor(t, func() bool { return r.sent(syscall.SIGKILL) })

	for _, call := range r.calls {
		if call.pid != -child.cmd.Process.Pid {
			t.Errorf("signal %v went to pid %d, want the group %d",
				call.sig, call.pid, -child.cmd.Process.Pid)
		}
	}
}

// The part a plain boolean could not give us. Asking whether the leader has been reaped and then
// signalling leaves a window between the two, and a window of microseconds is still a window.
//
// This races a real reap against the guard and checks the invariant from inside the guarded section
// itself, where it holds or the lock does not work. Driven from the test goroutine rather than
// through stopWith, because stopWith's escalation has no handle to join on and a test that reads a
// variable a detached goroutine may still write proves nothing about either.
func TestTheReapCannotHappenBetweenTheCheckAndTheSignal(t *testing.T) {
	for i := 0; i < 200; i++ {
		cmd := exec.Command("sleep", "0")
		Contain(cmd)
		if err := cmd.Start(); err != nil {
			t.Skipf("could not start a test process: %v", err)
		}
		child := Started(cmd)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); _ = child.Wait() }()

		// Keep asking until the reap lands. Every call that gets through has to see an unreaped
		// leader, because that is the only condition under which the pid is still ours to address.
		//
		// Bounded by a deadline rather than left to run until the guard closes, so that a guard which
		// never closes fails here and says so instead of hanging the package.
		deadline := time.Now().Add(2 * time.Second)
		for child.alive(func(int) {
			if child.reaped {
				t.Fatalf("run %d: the guard admitted a signal after the leader was waited on", i)
			}
		}) {
			if time.Now().After(deadline) {
				t.Fatalf("run %d: the guard never closed, so the reap is not being observed at all", i)
			}
		}
		wg.Wait()
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

// waitFor polls until a condition holds, for the escalation goroutine which has no handle to join on.
func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the escalation")
}
