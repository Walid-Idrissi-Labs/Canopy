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
