package exec

// A started command, and the one fact that makes stopping it safely possible.
//
// A process group is named by its leader's pid, and the kernel holds that number back only while the
// leader is still there. Once the leader has been waited on, the number goes back into circulation
// and can be handed to somebody else's job within milliseconds on a busy machine. A group signal sent
// after that point does not miss. It lands somewhere else, and somewhere else means every process in
// whatever group got the number next.
//
// So the rule this type exists to enforce is: never signal a group after its leader has been reaped.
// Nothing else here is subtle. What makes it worth a type rather than a boolean is that the check and
// the signal have to be one indivisible step. Asking "has it been reaped" and then signalling leaves a
// window between the two, and a window of microseconds is still a window, so both happen under the
// same lock that the reap itself takes.
//
// See D-37 for why this is a complete answer rather than a narrowing, and for what it gives up.

import (
	"os/exec"
	"sync"
)

// Child is a command that has been started, together with everything it started.
type Child struct {
	cmd *exec.Cmd

	// mu guards reaped and is held across any signal, so that no signal can be sent on the far side
	// of a reap that began after the check.
	mu     sync.Mutex
	reaped bool
}

// Contain puts a command in its own process group, and must be called before Start.
//
// Without it the command shares Canopy's own group, which makes a group kill either a no-op or an
// attempt to kill Canopy.
func Contain(cmd *exec.Cmd) { setProcessGroup(cmd) }

// Started returns a Child for a command that has already been started.
func Started(cmd *exec.Cmd) *Child { return &Child{cmd: cmd} }

// Wait reaps the command and records that it has been reaped.
//
// The flag is set under the lock before Wait's error reaches the caller, so a caller that acts on the
// result cannot race a signaller into the window this type exists to close.
func (c *Child) Wait() error {
	err := c.cmd.Wait()

	c.mu.Lock()
	c.reaped = true
	c.mu.Unlock()

	return err
}

// alive runs f with the process id, but only while that id is still this command's.
//
// Reports whether f ran. A false answer means the leader has already been waited on, so its pid may
// belong to somebody else by now and there is nothing here that can safely be signalled.
func (c *Child) alive(f func(pid int)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reaped || c.cmd.Process == nil {
		return false
	}
	f(c.cmd.Process.Pid)
	return true
}

// Stop ends the command and the group it leads.
//
// Returns as soon as the first signal is away. The escalation behind it runs on its own, because a
// process that ignores SIGTERM should not also hold up the caller for the grace period.
func (c *Child) Stop() { c.stop(afterGracePeriod()) }
