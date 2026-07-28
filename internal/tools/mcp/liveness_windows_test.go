//go:build windows

package mcp

import "os"

// stillRunning reports whether a process is alive, for the teardown tests.
//
// Windows has no signal zero. FindProcess is the check here rather than a formality, because unlike
// on unix it opens a handle and fails when there is no process to open one for.
func stillRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = process.Release()
	return true
}
