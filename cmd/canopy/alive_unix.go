//go:build unix

package main

import "syscall"

// processAlive reports whether a process exists, by signalling it with nothing.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
