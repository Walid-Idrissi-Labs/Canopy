//go:build darwin

package git

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile makes target a copy-on-write clone of source on APFS, which costs no disk and no time
// whatever the file's size. It reports false when cloning is not possible here, a different volume
// or a filesystem without clones, and the caller copies bytes instead.
func cloneFile(source, target string) bool {
	_ = os.Remove(target)
	return unix.Clonefile(source, target, unix.CLONE_NOFOLLOW) == nil
}
