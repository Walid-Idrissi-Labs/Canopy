//go:build windows

package exec

// Windows has no process-group signal to race with the reap. The mutex still keeps the Child state
// coherent for Stop, which targets only the os.Process handle on this platform.
func (c *Child) Wait() error {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.reaped = true
	c.mu.Unlock()
	return err
}
