package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/screens"
)

// golden compares a whole screen, colours stripped, with its stored snapshot; see the chat
// package's golden for why. CANOPY_UPDATE_GOLDENS=1 writes the snapshots again.
func golden(t *testing.T, name, got string) {
	t.Helper()
	if err := screens.Keep(name, got); err != nil {
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

// The first screen, whole: header, body and footer, at the three sizes people use.
func TestGoldenTheFirstScreen(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 50}} {
		store := fake.New()
		var model tea.Model = tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
		model, _ = model.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		golden(t, fmt.Sprintf("first-screen-%dx%d", size.w, size.h), model.View().Content)
		store.Close()
	}
}

// The help overlay and the credentials screen, whole, at the three sizes.
func TestGoldenHelpAndKeys(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 50}} {
		for _, screen := range []struct {
			name string
			key  tea.KeyPressMsg
		}{
			{"help", keyText("?")},
			{"keys", keyCode('k', tea.ModCtrl)},
		} {
			store := fake.New()
			var model tea.Model = tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
			model, _ = model.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			model, _ = model.Update(screen.key)
			golden(t, fmt.Sprintf("%s-%dx%d", screen.name, size.w, size.h), model.View().Content)
			store.Close()
		}
	}
}
