//go:build unix && !linux

package exec

import "os/exec"

// dieWithParent has no equivalent on this platform; orderly shutdown and SIGHUP handling are what
// stop children here.
func dieWithParent(*exec.Cmd) {}
