//go:build !unix

package main

// processAlive cannot ask here, so a lock is treated as held.
func processAlive(int) bool { return true }
