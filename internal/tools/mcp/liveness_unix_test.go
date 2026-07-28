//go:build unix

package mcp

import "syscall"

// stillRunning reports whether a process is alive, for the teardown tests.
//
// The obvious spelling of this, os.FindProcess followed by Signal(nil), does not work and does not
// fail loudly either. Go rejects a nil signal with "unsupported signal type" before it reaches the
// kernel, so the answer was always false and every teardown test that used it returned on its first
// look without having checked anything at all.
//
// Signal zero is a real thing at the syscall layer, where it asks whether the process exists without
// disturbing it, which is what this needed all along.
func stillRunning(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
