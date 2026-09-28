//go:build !unix

package main

import (
	"errors"
	"os"
)

// openNoFollow opens a file to append to, refusing one that is a symbolic link when looked at. Not
// the single step the unix version is: a link put in place between the look and the open is
// followed. LIMITATIONS says so.
func openNoFollow(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("a symbolic link")
	}
	return os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
}

// singleLink cannot count names here; a file that opened as a regular one is taken as it is.
func singleLink(os.FileInfo) bool { return true }

// ownedBy cannot tell who owns a file here, so nothing is taken as owned: canopy serve, which needs
// a directory only its user can open, does not start rather than trust one it cannot check.
func ownedBy(os.FileInfo, int) bool { return false }

// lockExclusive has no lock to take here.
func lockExclusive(*os.File) error { return errors.New("no file locks on this platform") }
