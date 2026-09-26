package keys

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// addKey walks the wizard for an Anthropic key and returns the command storing it produced.
func addKey(m Model, name string) (Model, tea.Cmd) {
	m = key(m, "a")
	m = typeRunes(m, name)
	m = press(m, keyCode(tea.KeyEnter))
	m = press(m, keyCode(tea.KeyEnter))
	m = typeRunes(m, "sk-ant-typo")
	return m.Update(keyCode(tea.KeyEnter))
}

// A key is asked about the moment it is stored, and a refusal names it before any chat does.
func TestAStoredKeyIsCheckedAtOnce(t *testing.T) {
	var asked []string
	m := New(&stubStore{})
	m.SetCheck(func(name string) CheckResult {
		asked = append(asked, name)
		return CheckResult{Refused: true, Note: "HTTP 401"}
	})
	m, cmd := addKey(m, "claude")
	if cmd == nil {
		t.Fatal("storing a key asked nothing")
	}
	m, _ = m.Update(cmd())
	if len(asked) != 1 || asked[0] != "claude" {
		t.Fatalf("asked about %v", asked)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "refused claude: HTTP 401") {
		t.Fatalf("error %v", m.err)
	}
	if !strings.Contains(m.View(), "refused claude") {
		t.Fatal("the refusal is not on screen")
	}

	accepting := New(&stubStore{})
	accepting.SetCheck(func(string) CheckResult { return CheckResult{Accepted: true} })
	accepting, cmd = addKey(accepting, "claude")
	accepting, _ = accepting.Update(cmd())
	if accepting.err != nil || !strings.Contains(accepting.status, "accepted claude") ||
		!strings.Contains(accepting.status, "Stored") {
		t.Fatalf("status %q, error %v", accepting.status, accepting.err)
	}

	unknown := New(&stubStore{})
	unknown.SetCheck(func(string) CheckResult { return CheckResult{Note: "the endpoint lists no models"} })
	unknown, cmd = addKey(unknown, "local")
	unknown, _ = unknown.Update(cmd())
	if unknown.err != nil || !strings.Contains(unknown.status, "local was not checked: the endpoint lists no models") {
		t.Fatalf("status %q, error %v", unknown.status, unknown.err)
	}
}

// t checks the selected key again; with no check attached, nothing is claimed.
func TestTChecksTheSelectedKey(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{{Ref: core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible}}}}
	m := New(store)
	if _, cmd := m.Update(keyText("t")); cmd != nil {
		t.Fatal("a check ran with nothing to check with")
	}
	m.SetCheck(func(name string) CheckResult { return CheckResult{Accepted: name == "kimi"} })
	m, cmd := m.Update(keyText("t"))
	if cmd == nil {
		t.Fatal("t asked nothing")
	}
	m, _ = m.Update(cmd())
	if !strings.Contains(m.status, "accepted kimi") {
		t.Fatalf("status %q", m.status)
	}
	// An answer about a key no longer being asked about is dropped.
	m, _ = m.Update(checkDoneMsg{name: "other", result: CheckResult{Refused: true}})
	if m.err != nil {
		t.Fatal("a stale answer was shown")
	}
}

// p records the owner's own price, which the resolver prices the next turn with; empty forgets it,
// and something that is not a price is refused with the field left open.
func TestAPriceIsEnteredOnTheScreen(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{{Ref: core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible}}}}
	m := New(store)
	m = key(m, "p")
	m = typeRunes(m, "0.6 two")
	m = press(m, keyCode(tea.KeyEnter))
	if m.mode != modeRate || m.err == nil || !store.keys[0].Rate.IsZero() {
		t.Fatalf("a bad price was taken: mode %v, error %v", m.mode, m.err)
	}
	for range "0.6 two" {
		m = press(m, keyCode(tea.KeyBackspace))
	}
	m = typeRunes(m, "$0.6 2.5 0.15")
	m = press(m, keyCode(tea.KeyEnter))
	want := core.KeyRate{InputPerMTok: 0.6, OutputPerMTok: 2.5, CacheReadPerMTok: 0.15}
	if store.keys[0].Rate != want || m.mode != modeList || !strings.Contains(m.status, "your own rate") {
		t.Fatalf("rate %+v, mode %v, status %q", store.keys[0].Rate, m.mode, m.status)
	}
	// Opened again, the field holds the price; emptied, it is forgotten.
	m = key(m, "p")
	if m.draftRate != "0.6 2.5 0.15" {
		t.Fatalf("the field opened on %q", m.draftRate)
	}
	for range m.draftRate {
		m = press(m, keyCode(tea.KeyBackspace))
	}
	m = press(m, keyCode(tea.KeyEnter))
	if !store.keys[0].Rate.IsZero() || !strings.Contains(m.status, "unpriced") {
		t.Fatalf("rate %+v, status %q", store.keys[0].Rate, m.status)
	}
}
