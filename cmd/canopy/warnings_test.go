package main

import (
	"fmt"
	"os"
	"testing"
)

// What setup says on standard error is kept, as lines, and standard error is put back.
func TestSetupWarningsAreKept(t *testing.T) {
	before := os.Stderr
	stop := captureStderr()
	fmt.Fprintln(os.Stderr, "warning: tools are not available: no workspace")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "warning: verification is not running")
	lines := stop()
	if os.Stderr != before {
		t.Fatal("standard error was not put back")
	}
	if len(lines) != 2 || lines[0] != "warning: tools are not available: no workspace" {
		t.Fatalf("kept %q", lines)
	}
}
