//go:build !darwin && !linux

package sandbox

// Available reports whether commands can be confined here.
func Available() error { return ErrUnavailable }

// Wrap cannot confine anything on this platform.
func (p Policy) Wrap(string, []string) (string, []string, error) { return "", nil, ErrUnavailable }

// RunTrampoline is only used on Linux.
func RunTrampoline([]string) error { return ErrUnavailable }

// NetworkEnforced reports whether this machine can limit a command's network.
func NetworkEnforced() bool { return false }

// LoopbackKept reports whether a network limited to the proxy still reaches the loopback address.
func LoopbackKept() bool { return false }
