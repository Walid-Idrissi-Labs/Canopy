package agents_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/screens"
)

// golden compares a rendered screen, colours stripped, with its stored snapshot; see the chat
// package's golden. CANOPY_UPDATE_GOLDENS=1 writes the snapshots again.
func golden(t *testing.T, name, got string) {
	t.Helper()
	if err := screens.Keep("agents", name, got); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "golden", name+".txt")
	lines := strings.Split(plain(got), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	got = strings.Join(lines, "\n") + "\n"
	if os.Getenv("CANOPY_UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no snapshot for %s; run with CANOPY_UPDATE_GOLDENS=1 to write it", name)
	}
	if string(want) != got {
		t.Errorf("%s changed; if that is meant, run with CANOPY_UPDATE_GOLDENS=1.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// The agent list and the mosaic of eight, each state represented, at the sizes people use.
func TestGoldenTheAgentScreens(t *testing.T) {
	e := engine(
		status("parser", core.AgentWorking, "fix the tokeniser"),
		status("docs", core.AgentIdle, "update the README"),
		status("perf", core.AgentAwaitingPermission, "profile the hot loop"),
		status("flaky", core.AgentFailed, "find the flaky test"),
	)
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 50}} {
		m := model(e)
		m.SetSize(size.w, size.h)
		golden(t, fmt.Sprintf("list-%dx%d", size.w, size.h), m.Body())
		golden(t, fmt.Sprintf("mosaic-%dx%d", size.w, size.h), mosaic(eight(), size.w, size.h).Body())
	}
}
