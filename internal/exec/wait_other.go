//go:build !darwin && !linux && !windows

package exec

// Canopy supports macOS and Linux. Keep other builds compiling without claiming the kernel-level
// process-group guarantee implemented by wait_darwin.go and wait_linux.go.
func (c *Child) Wait() error {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.reaped = true
	c.mu.Unlock()
	return err
}
