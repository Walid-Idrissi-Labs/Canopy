//go:build darwin

package exec

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// Wait observes exit without reaping, then serializes the actual reap with every group signal.
//
// Darwin has no waitid WNOWAIT. EVFILT_PROC/NOTE_EXIT supplies the equivalent ordering: kqueue
// reports that the process exited while the unreaped leader still reserves its pid. Once observed,
// cmd.Wait returns promptly. Holding mu across that reap and the reaped flag makes every signal
// wholly before or wholly after the reap.
func (c *Child) Wait() error {
	queue, err := unix.Kqueue()
	if err != nil {
		return fmt.Errorf("watching child exit: %w", err)
	}
	defer func() { _ = unix.Close(queue) }()

	change := unix.Kevent_t{
		Ident:  uint64(c.cmd.Process.Pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	for {
		_, err = unix.Kevent(queue, []unix.Kevent_t{change}, nil, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		// ESRCH means the process exited between Start and registration. It has not been reaped—
		// this method is the only owner of Wait—so the pid is still reserved and it is safe to
		// proceed directly to the serialized reap.
		if err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("registering child exit: %w", err)
		}
		break
	}

	if err == nil {
		events := make([]unix.Kevent_t, 1)
		for {
			var count int
			count, err = unix.Kevent(queue, nil, events, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				return fmt.Errorf("waiting for child exit: %w", err)
			}
			if count > 0 {
				break
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	err = c.cmd.Wait()
	c.reaped = true
	return err
}
