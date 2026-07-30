package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reading this package's own source, for a promise that is only checkable that way.
//
// Whether a child is reaped through internal/exec or around it is a property of the code rather
// than of any value it computes: both spellings compile, both make the process go away, and the
// difference only shows up as somebody else's job dying on a busy machine. So the only way to hold
// it is to look, and a comment saying so is a comment a later edit walks past.

func goSourceIn(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	files := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		files[entry.Name()] = string(body)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files found in %s, so this test proves nothing", dir)
	}
	return files
}

// The bridge is reaped through internal/exec's Child, never through the raw command.
//
// Child exists to enforce one rule, written at the top of internal/exec/child.go: never signal a
// process group after its leader has been reaped, because the kernel holds the pid back only while
// the leader is unreaped and hands it to somebody else's job within milliseconds afterwards. Stop
// leaves an escalation waiting to SIGKILL the group once the grace period is up, and Child.Wait is
// the only thing that marks the leader reaped under the lock that guards that signal.
//
// So a stop path that calls cmd.Wait releases the pid while the escalation still believes the group
// is live, and 250 milliseconds later Canopy sends SIGKILL to whatever group inherited the number.
// It ran here for the whole of the phase that introduced this route, on every turn, and nothing
// caught it because the process it was meant to kill did die: what it also killed belonged to
// somebody else and was never anything this suite could observe.
func TestTheBridgeIsReapedThroughTheThingThatKnowsWhenItsPidStopsBeingIts(t *testing.T) {
	for name, body := range goSourceIn(t, ".") {
		if strings.Contains(body, "cmd.Wait()") {
			t.Errorf("%s reaps with cmd.Wait, which leaves Child.reaped false while Stop's "+
				"escalation is still armed, so the SIGKILL that follows lands on whichever process "+
				"group the kernel gave the pid to next. Use child.Wait instead. See D-37 and the "+
				"contract at the top of internal/exec/child.go", name)
		}
	}
}
