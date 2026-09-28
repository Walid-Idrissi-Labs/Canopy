//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func abi() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// Available reports whether commands can be confined here.
func Available() error {
	if abi() < 1 {
		return fmt.Errorf("%w: this kernel has no Landlock", ErrUnavailable)
	}
	return nil
}

// Wrap returns the command that runs name inside the policy, through Canopy's own trampoline.
func (p Policy) Wrap(name string, args []string) (string, []string, error) {
	if err := Available(); err != nil {
		return "", nil, err
	}
	// The running image itself, through /proc, rather than the path it was started from: Canopy
	// upgraded in place while it runs would otherwise be re-run as the new binary, reading a policy
	// the old one wrote.
	self := "/proc/self/exe"
	if _, err := os.Stat(self); err != nil {
		if self, err = os.Executable(); err != nil {
			return "", nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return "", nil, err
	}
	return self, append([]string{TrampolineArg, string(encoded), "--", name}, args...), nil
}

// RunTrampoline confines this process by the policy in args and executes the command after "--".
// It returns only on failure.
func RunTrampoline(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("sandbox trampoline: bad arguments")
	}
	var p Policy
	if err := json.Unmarshal([]byte(args[0]), &p); err != nil {
		return fmt.Errorf("sandbox trampoline: %w", err)
	}
	// Landlock and no_new_privs bind the calling thread. Locked from here to exec, or the runtime
	// could exec from another thread and the command would run unconfined without any error.
	runtime.LockOSThread()
	if err := p.restrict(); err != nil {
		return err
	}
	path, err := exec.LookPath(args[2])
	if err != nil {
		return err
	}
	return syscall.Exec(path, args[2:], os.Environ())
}

const writeAccess = unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
	unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
	unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
	unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM

func (p Policy) restrict() error {
	version := abi()
	if version < 1 {
		return fmt.Errorf("%w: no Landlock", ErrUnavailable)
	}
	handled := uint64(writeAccess)
	if version >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if version >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	switch {
	case version >= 4 && p.Network == NetworkNone:
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_CONNECT_TCP | unix.LANDLOCK_ACCESS_NET_BIND_TCP
	case version >= 4 && p.Network == NetworkProxy:
		// Landlock names ports, not addresses: connecting is allowed to the proxy's port only,
		// on any address, and a test's own servers on other ports cannot be reached.
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	}
	size := unsafe.Sizeof(attr)
	if version < 4 {
		size = unsafe.Offsetof(attr.Access_net)
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return fmt.Errorf("creating the Landlock ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer func() { _ = unix.Close(ruleset) }()

	fileAccess := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	if version >= 3 {
		fileAccess |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	paths := append(append([]string(nil), p.Writable...), p.Devices...)
	for _, dir := range paths {
		f, err := unix.Open(dir, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		access := handled
		var st unix.Stat_t
		if unix.Fstat(f, &st) == nil && st.Mode&unix.S_IFMT != unix.S_IFDIR {
			// A rule on a file may only grant rights that make sense for a file.
			access = fileAccess
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(f)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset),
			unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		_ = unix.Close(f)
		if errno != 0 {
			return fmt.Errorf("allowing %s: %w", dir, errno)
		}
	}
	if attr.Access_net&unix.LANDLOCK_ACCESS_NET_CONNECT_TCP != 0 && p.Network == NetworkProxy {
		rule := landlockNetPortAttr{AllowedAccess: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, Port: uint64(p.ProxyPort)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset),
			landlockRuleNetPort, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("allowing the proxy's port: %w", errno)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("setting no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("applying Landlock: %w", errno)
	}
	return nil
}

// landlockNetPortAttr is the kernel's struct landlock_net_port_attr, which x/sys does not define.
type landlockNetPortAttr struct {
	AllowedAccess uint64
	Port          uint64
}

// landlockRuleNetPort is LANDLOCK_RULE_NET_PORT.
const landlockRuleNetPort = 2

// NetworkEnforced reports whether this machine can limit a command's network: Landlock ABI 4,
// Linux 6.7 and later.
func NetworkEnforced() bool { return abi() >= 4 }

// LoopbackKept reports whether a network limited to the proxy still reaches the loopback address;
// Landlock limits by port, so a local server on another port is cut off.
func LoopbackKept() bool { return false }
