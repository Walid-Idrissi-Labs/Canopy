//go:build !darwin && !linux

package git

func cloneFile(string, string) bool { return false }
