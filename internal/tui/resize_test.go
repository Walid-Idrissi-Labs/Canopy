package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// A9-02's second clause: resize does not corrupt the frame.
//
// Applied in sequence to one model rather than to a fresh one per size, which is the whole point and
// is what every existing size test was missing: the tests that looked like resize tests rebuilt the
// application inside the loop, so nothing ever carried state across a size change. A model that
// cached a width, or held a scroll position measured in the old geometry, would pass all of them and
// still draw a broken frame on the first drag of a window edge.
func TestResizingInSequenceKeepsEveryLineInsideTheFrame(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")

	// Wide to narrow to wide again, across the wordmark threshold in both directions, and through
	// the smallest size the application will draw at.
	sizes := []struct{ w, h int }{
		{120, 40}, {80, 24}, {200, 60}, {62, 14}, {100, 30}, {80, 24}, {160, 50},
	}

	for _, size := range sizes {
		model, _ = model.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		view := model.View()

		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Fatalf("at %dx%d, line %d is %d columns wide:\n%q",
					size.w, size.h, i, got, plain(line))
			}
		}

		// The frame is the header, the body and the footer, and it has to fit the terminal it was
		// last told about. A view taller than the screen scrolls the header away, which is the same
		// failure as a line too wide, one axis over.
		if got := len(strings.Split(view, "\n")); got > size.h {
			t.Fatalf("at %dx%d the view is %d rows tall", size.w, size.h, got)
		}
	}
}

// The frame clips an over-tall body rather than letting it push the header off the top and the
// footer off the bottom. Reached here by shrinking to the smallest usable terminal, where the
// screens that compose several panels at once have more rows than they have room for.
func TestAShortTerminalStillDrawsItsHeaderAndFooter(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 12})

	view := plain(model.View())
	lines := strings.Split(view, "\n")
	if len(lines) > 12 {
		t.Fatalf("the view is %d rows tall on a 12 row terminal:\n%s", len(lines), view)
	}
	if !strings.Contains(lines[0], "canopy") && !strings.Contains(lines[0], "─") &&
		!strings.Contains(lines[0], "╭") {
		t.Errorf("the first row is not the header:\n%s", view)
	}
	if strings.TrimSpace(lastLine(view)) == "" {
		t.Errorf("the footer was pushed off the bottom:\n%s", view)
	}
}
