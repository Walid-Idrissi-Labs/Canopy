package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// runServe keeps this project's agents running without a client attached: it serves the engine
// over the Agent Client Protocol on a unix socket only this user can reach, and each connection is
// a client that can start, pick up, prompt and answer for a conversation. Closing a client leaves
// its agents working; `canopy attach` picks them up again.
func runServe(args []string, errOut io.Writer) int {
	host, code := openHost("serve", args, errOut)
	if host == nil {
		return code
	}
	defer host.close()
	path, err := serveSocket(host.dir)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	listener, err := listenServe(path)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	defer func() { _ = os.Remove(path) }()
	host.hub.Waiting = func(sessionID string) {
		code := session.Code(sessionID)
		_, _ = fmt.Fprintf(errOut, "%s conversation %s is waiting on you: canopy attach %s\n",
			time.Now().Format("15:04:05"), code, code)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	_, _ = fmt.Fprintf(errOut, "canopy serve: %s on %s\n", host.dir, path)
	var wg sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = conn.Close() }()
			_ = host.hub.Serve(ctx, conn, conn)
		}()
	}
	wg.Wait()
	return exitOK
}

// serveSocket is where canopy serve listens for a project: a directory only this user can open,
// under the runtime or temporary directory, and a file named for the workspace. Short on purpose,
// since a unix socket's path is limited to about a hundred bytes.
func serveSocket(dir string) (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	root := filepath.Join(base, fmt.Sprintf("canopy-%d", os.Getuid()))
	// One project reached through a link and through its real path is one server.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	path := filepath.Join(root, gitpkg.WorkspaceID(dir)+".sock")
	if len(path) > 100 {
		return "", fmt.Errorf("%s is too long for a unix socket; set XDG_RUNTIME_DIR to a shorter directory", path)
	}
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	// On a shared /tmp someone else could have made it first, or put a link there.
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !privateDir(info, os.Getuid()) {
		return "", fmt.Errorf("%s is not a directory only you can open, so canopy serve will not use it", root)
	}
	return path, nil
}

// privateDir reports whether info, from Lstat, is a real directory owned by uid that nobody else
// can open.
func privateDir(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm() == 0o700 &&
		int(stat.Uid) == uid
}

// errServing is a server already running for the project.
var errServing = errors.New("canopy serve is already running for this project; canopy attach connects to it")

// listenServe listens on path, clearing a socket left behind by a server that is gone and refusing
// to start beside one that is still there. A lock beside the socket, held for as long as the
// listener is open, settles two servers started at the same moment.
func listenServe(path string) (net.Listener, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, errServing
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		_ = lock.Close()
		return nil, errServing
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = lock.Close()
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, err
	}
	return lockedListener{Listener: listener, lock: lock}, nil
}

// lockedListener releases the lock when the listener closes.
type lockedListener struct {
	net.Listener
	lock *os.File
}

func (l lockedListener) Close() error {
	err := l.Listener.Close()
	_ = l.lock.Close()
	return err
}
