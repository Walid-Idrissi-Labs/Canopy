//go:build unix

package theme

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A pipe or a device among the theme files is refused without being read: reading one can wait
// forever, or never end, and the theme list is read while the interface waits.
func TestAPipeOrADeviceIsNotRead(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.json"), 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero.json")); err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	go func() {
		reload(t, dir)
		done <- Problems()
	}()
	select {
	case got := <-done:
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "pipe.json") || !strings.Contains(joined, "zero.json") {
			t.Fatalf("problems:\n%s", joined)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading the theme files hung on a pipe or a device")
	}
}
