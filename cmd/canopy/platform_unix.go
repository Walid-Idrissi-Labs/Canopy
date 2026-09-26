//go:build unix

package main

import (
	"os"
	"syscall"
)

// openNoFollow opens a file to append to, refusing a symbolic link at the last step of the path.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
}

// singleLink reports whether an opened file has exactly one name, so writing to it writes nowhere
// else.
func singleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

// ownedBy reports whether a file belongs to the user with this id.
func ownedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}

// lockExclusive takes a lock on a file that only one process can hold, without waiting for it.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
