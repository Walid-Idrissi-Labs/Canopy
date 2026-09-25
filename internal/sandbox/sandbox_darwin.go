//go:build darwin

package sandbox

import (
	"fmt"
	"os"
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
	if len(p.DenyWrite)+len(p.DenyWriteExact) > 0 {
		b.WriteString("(deny file-write*\n")
		for _, path := range p.DenyWrite {
			fmt.Fprintf(&b, "  (subpath %s)\n", quote(path))
		}
		for _, path := range p.DenyWriteExact {
			fmt.Fprintf(&b, "  (literal %s)\n", quote(path))
		}
		b.WriteString(")\n")
	}
	if len(p.DenyRead) > 0 {
		b.WriteString("(deny file-read*\n")
		for _, path := range p.DenyRead {
			fmt.Fprintf(&b, "  (subpath %s)\n", quote(path))
		}
		b.WriteString(")\n")
	}
	if p.Network == NetworkNone {
		b.WriteString("(deny network-outbound (remote ip))\n(deny network-bind)\n")
	}
	return b.String()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// RunTrampoline is only used on Linux.
func RunTrampoline([]string) error { return ErrUnavailable }
