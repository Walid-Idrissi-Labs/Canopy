//go:build linux

package exec

import (
	"errors"

	"golang.org/x/sys/unix"
)

// Wait observes exit without reaping, then serializes the actual reap with every group signal.
//
// waitid with WNOWAIT is the missing kernel primitive: it blocks until the leader exits but leaves
// the zombie and therefore its pid reserved. Once it reports exit, cmd.Wait returns promptly. Holding
// mu across that reap and the reaped flag means a signal is either wholly before the reap, while the
// id is still reserved for this child, or wholly after the flag and is refused.
func (c *Child) Wait() error {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, c.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.cmd.Wait()
	c.reaped = true
	return err
}
