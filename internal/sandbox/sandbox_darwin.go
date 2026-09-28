//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Available reports whether commands can be confined here.
func Available() error {
	if _, err := os.Stat(sandboxExec); err != nil {
		return fmt.Errorf("%w: %s is missing", ErrUnavailable, sandboxExec)
	}
	return nil
}

// Wrap returns the command that runs name inside the policy.
func (p Policy) Wrap(name string, args []string) (string, []string, error) {
	if err := Available(); err != nil {
		return "", nil, err
	}
	return sandboxExec, append([]string{"-p", p.profile(), name}, args...), nil
}

// profile is the Seatbelt profile for the policy: everything allowed, then writes denied except
// beneath the writable directories, reads of credential paths denied, and the network denied when
// asked.
func (p Policy) profile() string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n(allow file-write*\n")
	for _, dir := range p.Writable {
		fmt.Fprintf(&b, "  (subpath %s)\n", quote(dir))
	}
	for _, dev := range p.Devices {
		fmt.Fprintf(&b, "  (literal %s)\n", quote(dev))
	}
	b.WriteString("  (literal \"/dev/stdout\") (literal \"/dev/stderr\") (regex #\"^/dev/fd/\"))\n")
	// Seatbelt applies the last rule that matches. A denied directory that holds a writable one,
	// Canopy's own data directory holding the agents' worktrees, is denied first, the writable
	// directories inside it are allowed again after, and every other deny comes last, so a guard
	// inside the workspace (a nested repository's hooks) still holds over the re-allowed workspace.
	covering := func(path string) bool {
		for _, dir := range p.Writable {
			if dir == path || strings.HasPrefix(dir, strings.TrimSuffix(path, "/")+"/") {
				return true
			}
		}
		return false
	}
	var coverWrite, coverRead, otherWrite, otherRead []string
	for _, path := range p.DenyWrite {
		if covering(path) {
			coverWrite = append(coverWrite, path)
		} else {
			otherWrite = append(otherWrite, path)
		}
	}
	for _, path := range p.DenyRead {
		if covering(path) {
			coverRead = append(coverRead, path)
		} else {
			otherRead = append(otherRead, path)
		}
	}
	subpaths := func(verb string, paths []string) {
		if len(paths) == 0 {
			return
		}
		fmt.Fprintf(&b, "(%s\n", verb)
		for _, path := range paths {
			fmt.Fprintf(&b, "  (subpath %s)\n", quote(path))
		}
		b.WriteString(")\n")
	}
	subpaths("deny file-write*", coverWrite)
	subpaths("deny file-read*", coverRead)
	if len(coverWrite)+len(coverRead) > 0 {
		var inside []string
		for _, dir := range p.Writable {
			for _, path := range append(append([]string(nil), coverWrite...), coverRead...) {
				if dir == path || strings.HasPrefix(dir, strings.TrimSuffix(path, "/")+"/") {
					inside = append(inside, dir)
					break
				}
			}
		}
		subpaths("allow file-read*", inside)
		subpaths("allow file-write*", inside)
		// Reaching it means looking each directory above it up by name. That much, and no listing
		// or reading of what else they hold, is allowed on the way down.
		var above []string
		for _, dir := range inside {
			for parent := filepath.Dir(dir); parent != dir && parent != "/"; dir, parent = parent, filepath.Dir(parent) {
				above = append(above, parent)
			}
		}
		if len(above) > 0 {
			b.WriteString("(allow file-read-metadata\n")
			for _, dir := range above {
				fmt.Fprintf(&b, "  (literal %s)\n", quote(dir))
			}
			b.WriteString(")\n")
		}
	}
	if len(otherWrite)+len(p.DenyWriteExact)+len(p.DenyWriteMatching) > 0 {
		b.WriteString("(deny file-write*\n")
		for _, path := range otherWrite {
			fmt.Fprintf(&b, "  (subpath %s)\n", quote(path))
		}
		for _, path := range p.DenyWriteExact {
			fmt.Fprintf(&b, "  (literal %s)\n", quote(path))
		}
		for _, pattern := range p.DenyWriteMatching {
			fmt.Fprintf(&b, "  (regex #%s)\n", quote(pattern))
		}
		b.WriteString(")\n")
	}
	subpaths("deny file-read*", otherRead)
	switch p.Network {
	case NetworkNone:
		b.WriteString("(deny network-outbound (remote ip))\n(deny network-bind)\n")
	case NetworkProxy:
		// The loopback address only, where the proxy is and where a test's own servers listen.
		b.WriteString("(deny network-outbound (remote ip))\n(allow network-outbound (remote ip \"localhost:*\"))\n")
	}
	return b.String()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// RunTrampoline is only used on Linux.
func RunTrampoline([]string) error { return ErrUnavailable }

// NetworkEnforced reports whether this machine can limit a command's network; Seatbelt always can.
func NetworkEnforced() bool { return true }

// LoopbackKept reports whether a network limited to the proxy still reaches the loopback address;
// Seatbelt limits by address, so it does.
func LoopbackKept() bool { return true }
