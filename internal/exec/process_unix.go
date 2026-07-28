//go:build unix

package exec

import (
	"os/exec"
	"syscall"
	"time"
)

// Process group handling, which is the whole reason cancellation works here.
//
// A test runner spawns workers, a dev server spawns a bundler, a shell script spawns whatever it
// likes. Killing only the process we started leaves all of those running, holding ports and file
// handles, and the next run fails with "address already in use" for reasons nobody can see. Putting
// the command in its own group and killing the group takes the children with it.

// setProcessGroup puts the command in a new process group of its own.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalFunc is syscall.Kill under another name, so the escalation below can be driven in a test
// without anything being signalled for real.
type signalFunc func(pid int, sig syscall.Signal) error

// killGroup terminates the command and everything it started.
//
// A negative PID addresses the group rather than the process, which is the entire mechanism. It is
// also why the group has to be set at start: without Setpgid the command shares Canopy's own group,
// and a negative kill would take Canopy down with it.
//
// reaped is closed once the command has been waited on. See escalate for why the second signal is
// not allowed to ignore it.
func killGroup(cmd *exec.Cmd, reaped <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid

	// Terminate first, so a process with cleanup to do gets the chance. A test runner asked to stop
	// politely removes its temporary directories; one that is killed outright leaves them.
	if oursToSignal(pid, reaped, syscall.Kill) {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}

	go func() {
		escalate(pid, reaped, afterGracePeriod(), syscall.Kill)

		// The process on its own as well as the group, as a fallback for the case where the group
		// was never established and -pid therefore names nothing. Safe to call this late because
		// os.Process refuses to signal a pid it has already waited on, which is exactly the
		// guarantee the raw syscalls above do not have.
		_ = cmd.Process.Kill()
	}()
}

// oursToSignal reports whether -pid still names this command's process group.
//
// The question exists because a process group is named by its leader's pid, and the kernel holds
// that number back only for as long as the group still has a member in it. Once the last one exits,
// the number goes back into circulation and can be handed to somebody else's job within
// milliseconds on a busy machine. A signal sent to it after that point does not miss, it lands
// somewhere else, and "somewhere else" here means every process in a group started by a shell that
// happened to get the number next.
//
// Asked twice on the way through a kill, before each signal, rather than answered once and cached.
// The window it closes is the interval between the two, and a stale answer would be exactly as
// wrong as no answer.
//
// **This narrows the window and does not close it, and the residual case cannot be closed with
// kill(2).** Two things are left. The reaped branch asks whether a group with this id exists and
// takes yes to mean it is still ours, which it cannot distinguish from the id having been reissued
// to somebody else. And whichever answer comes back, the last member of the group can exit between
// this returning and the caller signalling, so even a correct answer can be stale by the time it is
// used. Both need an identifier the kernel will not recycle, which on Linux means a pidfd and on
// darwin, which is what Canopy is developed on, means nothing that exists.
//
// It is kept because the alternative is worse in the ordinary case rather than because it is
// complete. Not signalling at all after the leader is reaped would close both holes and leave every
// orphaned child of every cancelled test run alive, holding the ports that make the next run fail.
// The failure this guards against needs the group to empty in a window of microseconds and the
// number to be handed straight to somebody else; the failure it prevents happens every time a test
// runner spawns workers and is cancelled.
func oursToSignal(pid int, reaped <-chan struct{}, signal signalFunc) bool {
	select {
	case <-reaped:
		// The leader has been waited on, so the id is reserved only if something else in the group
		// is still holding it. Signal zero asks without touching anything.
		return signal(-pid, 0) == nil
	default:
		// Unreaped, so the leader is still there and the kernel cannot have given the id away.
		return true
	}
}

// escalate follows SIGTERM with SIGKILL, because a process that ignores the first is exactly the
// process this exists to deal with.
//
// It stops waiting the moment the command is reaped, rather than serving out the grace period
// regardless. Waiting on a command does not return until everything holding its output has gone
// with it, so in the ordinary case there is nothing left by then and the check below says so.
// Anything that is left has already had its SIGTERM and has outlived its own parent, which is the
// orphan this file exists to prevent, so it gets the second signal at once rather than after a
// grace period it has already been given.
func escalate(pid int, reaped <-chan struct{}, grace <-chan time.Time, signal signalFunc) {
	select {
	case <-reaped:
	case <-grace:
	}

	if !oursToSignal(pid, reaped, signal) {
		return
	}
	_ = signal(-pid, syscall.SIGKILL)
}
