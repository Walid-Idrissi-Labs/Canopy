//go:build windows

package exec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Stopping a command ends what it started too: a batch file starts a second one with `start /b`,
// which keeps appending to a file, and after Stop the file stops growing. Without the job object the
// second one outlives the first, which is what killing the process alone did.
func TestStoppingACommandEndsWhatItStarted(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "ticks.txt")
	child := filepath.Join(dir, "child.bat")
	parent := filepath.Join(dir, "parent.bat")
	if err := os.WriteFile(child, []byte("@echo off\r\n:loop\r\necho tick>>\""+out+"\"\r\nping -n 2 127.0.0.1 >nul\r\ngoto loop\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("@echo off\r\nstart /b cmd /c \""+child+"\"\r\nping -n 120 127.0.0.1 >nul\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd", "/c", parent)
	Contain(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	started := Started(cmd)
	waited := make(chan struct{})
	go func() {
		_ = started.Wait()
		close(waited)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		if info, err := os.Stat(out); err == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the started batch file never ran")
		}
		time.Sleep(100 * time.Millisecond)
	}
	started.Stop()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not end")
	}
	time.Sleep(2 * time.Second)
	before, _ := os.Stat(out)
	time.Sleep(4 * time.Second)
	after, _ := os.Stat(out)
	if after.Size() != before.Size() {
		t.Fatalf("what the command started is still running: %d bytes became %d", before.Size(), after.Size())
	}
}
