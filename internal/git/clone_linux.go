//go:build linux

package git

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile makes target a reflink of source on filesystems that support it (Btrfs, XFS, bcachefs),
// and reports false elsewhere so the caller copies bytes instead.
func cloneFile(source, target string) bool {
	in, err := os.Open(source)
	if err != nil {
		return false
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		_ = out.Close()
		return false
	}
	return out.Close() == nil
}
