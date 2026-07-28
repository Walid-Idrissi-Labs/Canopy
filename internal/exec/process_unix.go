//go:build unix

package exec

import (
	"os/exec"
	"syscall"
	"time"
)

// Where the signals go, which is the whole reason cancellation works here.
//
// A test runner spawns workers, a dev server spawns a bundler, a shell script spawns whatever it
// likes. Killing only the process we started leaves all of those running, holding ports and file
// handles, and the next run fails with "address already in use" for reasons nobody can see. Putting
// the command in its own group and killing the group takes the children with it.
//
// A negative pid addresses the group rather than the process, which is the entire mechanism. When it
// is safe to use one is Child's problem, and every signal below goes through Child.alive so that the
// answer cannot go stale between the check and the signal.

// setProcessGroup puts the command in a new process group of its own.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalFunc is syscall.Kill under another name, so the escalation can be driven in a test without
// anything being signalled for real.
type signalFunc func(pid int, sig syscall.Signal) error

func (c *Child) stop(grace <-chan time.Time) { c.stopWith(syscall.Kill, grace) }

// stopWith sends SIGTERM, then SIGKILL once the grace period is up.
//
// Terminate first, so a process with cleanup to do gets the chance: a test runner asked to stop
// politely removes its temporary directories, and one that is killed outright leaves them.
//
// Both signals go through alive, and a false answer is the end of it rather than a reason to look for
// another way to reach the group. There is no probe here asking whether a group still exists, because
// the answer to that question cannot distinguish "still ours" from "reissued to somebody else", and a
// yes that means the second one is the failure this is guarding against rather than a check for it.
func (c *Child) stopWith(signal signalFunc, grace <-chan time.Time) {
	c.alive(func(pid int) { _ = signal(-pid, syscall.SIGTERM) })

	go func() {
		<-grace

		if c.alive(func(pid int) { _ = signal(-pid, syscall.SIGKILL) }) {
			return
		}

		// Either it has been reaped, in which case there is nothing owed and nothing safe to signal,
		// or the group was never established and -pid names nothing. The process on its own covers
		// the second: os.Process refuses to signal a pid it has already waited on, which is the
		// guarantee the raw syscall above does not have.
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}()
}
