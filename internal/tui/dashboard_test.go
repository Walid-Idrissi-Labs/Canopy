package tui_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// ansi matches colour escape sequences, so tests can assert on what a user actually reads rather
// than on how it was painted.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// nextMsg runs a Bubble Tea command and returns its message, failing rather than hanging.
//
// The commands under test block on the event channel, so a subscription mistake shows up as a
// test that never finishes. A bounded wait turns that into a normal failure with a useful message.
func nextMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an event, the subscription is probably wrong")
		return nil
	}
}

func key(m tea.Model, k string) tea.Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	return next
}

// The P1-07 acceptance criterion.
func TestRendersTheFourWorkspaces(t *testing.T) {
	store := fake.New()
	defer store.Close()

	view := plain(tui.New(store).View())

	for _, name := range []string{"feat-login", "fix-cache", "refactor-api", "spike-search"} {
		if !strings.Contains(view, name) {
			t.Errorf("workspace %q is missing from the dashboard:\n%s", name, view)
		}
	}
}

// allowedOutside is what each interface package may import from beyond internal/core, and why.
//
// One entry per package rather than one list shared by all of them, so an import that is defensible
// on one screen does not become defensible on every screen by having been written down once. A
// package with no entry may import internal/core and other internal/tui packages and nothing else,
// which is the rule as it was originally written.
//
// The entries below are a description of what is true today and not an endorsement of it. Two of
// them are genuine engine dependencies that predate this table, and writing them down is what makes
// them visible enough to be argued with; the previous arrangement made them invisible, because the
// check walked one directory and the packages holding them were not in it.
var allowedOutside = map[string]map[string]string{
	"chat": {
		"internal/session": "the conversation is a session.Prompt and a session.CompactionResult, " +
			"which reach this screen as values rather than as an engine it calls",
		"internal/permission": "a permission request is what the question panel draws",
		"internal/config":     "the slash commands a project declares",
	},
	"agents": {
		"internal/session": "an agent and its status are session types, drawn rather than driven",
	},
	"keys": {
		"internal/catalog": "the model lineups, which are a dated table of strings with no engine " +
			"behind them: no I/O, no network and no credential, so swapping the fake for the real " +
			"engine does not touch it",
	},
}

// The real architectural constraint: the interface depends on the contract and on other interface
// packages, and on no engine package. That is what lets the fake be swapped for the real engine
// without any of this changing, and a test is the only thing keeping it true, because the first
// accidental import compiles perfectly well.
//
// Sibling UI packages are allowed. Screens have to be composed somewhere, and app.go importing
// internal/tui/keys says nothing about where state comes from. Importing internal/keys,
// internal/git or internal/provider would, which is what this actually forbids.
//
// It walked one directory and did not recurse, which meant the rule applied to the package that
// already obeyed it and to none of the packages where a screen is actually written. internal/tui/keys
// was not held to it and had been importing internal/catalog for two phases with nothing to notice.
// That mattered the moment a sign-in arrived: a screen that reached internal/keys or internal/provider
// to sign somebody in is exactly what this forbids, and the check would have said nothing.
//
// Replaces TestDashboardOnlyDependsOnCore, which is the same check over one directory. Renamed
// rather than kept, because "dashboard" was the name of the one package it happened to cover.
func TestEveryInterfacePackageDependsOnTheContractAndNotOnTheEngine(t *testing.T) {
	const own = "github.com/Walid-Idrissi-Labs/Canopy/"
	const uiPrefix = own + "internal/tui/"

	checked := 0
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			// testdata is a convention the toolchain already ignores, and a fixture is not the
			// interface.
			if name == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		checked++

		// The package's own name inside internal/tui, which is what the table above is keyed on.
		// The root is the dashboard and has no entry, so it is held to core alone.
		pkg := filepath.ToSlash(filepath.Dir(path))
		if pkg == "." {
			pkg = ""
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		for _, imp := range file.Imports {
			value, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("%s: bad import %s", path, imp.Path.Value)
			}
			if !strings.HasPrefix(value, own) {
				continue // stdlib and third party are fine
			}
			if value == own+"internal/core" || strings.HasPrefix(value, uiPrefix) {
				continue
			}
			if _, ok := allowedOutside[pkg][strings.TrimPrefix(value, own)]; ok {
				continue
			}
			t.Errorf("%s imports %q. The interface may depend on internal/core, on other "+
				"internal/tui packages, and on what allowedOutside records for its own package, and "+
				"on nothing else. Reaching internal/keys or internal/provider from a screen is what "+
				"this exists to stop: it is how the interface stops being swappable between the fake "+
				"and the real engine, and it is how a credential ends up one mistake away from a "+
				"rendered frame.", path, value)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the interface: %v", err)
	}

	// A check that silently inspects nothing would pass forever. Counted across the whole tree now,
	// so a walk that stopped recursing would be caught rather than reported as a clean run.
	if checked < 20 {
		t.Fatalf("only %d source files were checked, so this is not covering the interface", checked)
	}
	for _, pkg := range []string{"keys", "chat"} {
		if _, err := os.Stat(pkg); err != nil {
			t.Errorf("internal/tui/%s was not walked: %v", pkg, err)
		}
	}
}

// The thing the product exists to show, seen through the interface rather than through the state.
func TestEditingAWorkspaceTurnsItStaleOnScreen(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)

	// Take the waiting command before making the edit. It is bound to this model's subscription,
	// and the fake keeps no history, so an edit published before anyone is listening is simply
	// gone. Subscribing afterwards waits forever for an event that already happened.
	waiting := model.(tui.Model).Init()

	before := plain(model.View())
	if !strings.Contains(before, "PASS") {
		t.Fatalf("expected a passing workspace to start with:\n%s", before)
	}
	if strings.Contains(before, "STALE") {
		t.Fatalf("nothing should be stale yet:\n%s", before)
	}

	if err := store.Touch("ws-refactor-api"); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	model, _ = model.Update(nextMsg(t, waiting))

	after := plain(model.View())
	if !strings.Contains(after, "STALE") {
		t.Errorf("the edited workspace should read as stale:\n%s", after)
	}
	if strings.Contains(after, "PASS  ") && !strings.Contains(after, "STALE") {
		t.Error("the dashboard did not update")
	}
}

// From DECISIONS.md D-10 and the P4-09 acceptance criterion, checked early because retrofitting a
// visual language is far more expensive than starting with one. Strip the colour and every state
// still has to be tellable apart.
func TestStatesAreDistinguishableWithoutColour(t *testing.T) {
	store := fake.New()
	defer store.Close()

	view := plain(tui.New(store).View())
	if strings.Contains(view, "\x1b[") {
		t.Fatal("the plain view still contains escape sequences, the test is not testing anything")
	}

	// Three different states are on screen in the seeded fake, and each has to be readable as a
	// word rather than inferred from a colour that is no longer there.
	for _, want := range []string{"PASS", "FAIL", "NOT SET"} {
		if !strings.Contains(view, want) {
			t.Errorf("state %q is not readable without colour:\n%s", want, view)
		}
	}
}

// A green roll-up must never be the only thing on screen. The reason for it has to be there too,
// because a status nobody can account for is a status nobody should trust.
func TestSelectedRowExplainsItself(t *testing.T) {
	store := fake.New()
	defer store.Close()

	view := plain(tui.New(store).View())
	if !strings.Contains(view, "all required evidence is current and passing") {
		t.Errorf("the dashboard should explain the selected workspace's verdict:\n%s", view)
	}
}

func TestFailingWorkspaceExplainsWhy(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)
	model = key(model, "j") // fix-cache, the failing one

	view := plain(model.View())
	if !strings.Contains(view, "unit") {
		t.Errorf("the reason should name the test that failed:\n%s", view)
	}
}

func TestNavigation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)

	selected := func(m tea.Model) string {
		w, ok := m.(tui.Model).SelectedWorkspace()
		if !ok {
			t.Fatal("nothing selected")
		}
		return w.Name
	}

	if got := selected(model); got != "feat-login" {
		t.Errorf("initial selection = %q, want feat-login", got)
	}

	model = key(model, "j")
	if got := selected(model); got != "fix-cache" {
		t.Errorf("after j, selection = %q, want fix-cache", got)
	}

	model = key(model, "k")
	if got := selected(model); got != "feat-login" {
		t.Errorf("after k, selection = %q, want feat-login", got)
	}

	// Moving past the top must not wrap or go out of range.
	model = key(model, "k")
	if got := selected(model); got != "feat-login" {
		t.Errorf("moving up from the first row = %q, want feat-login", got)
	}

	model = key(model, "G")
	if got := selected(model); got != "spike-search" {
		t.Errorf("after G, selection = %q, want spike-search", got)
	}

	model = key(model, "j")
	if got := selected(model); got != "spike-search" {
		t.Errorf("moving down from the last row = %q, want spike-search", got)
	}
}

// Selection is held by workspace ID, not row index. A worktree removed outside Canopy shifts every
// row below it, and an index would then quietly point at a different workspace than the one the
// user chose. In a tool built on being trustworthy, acting on the wrong workspace is about the
// worst bug available.
func TestSelectionFollowsTheWorkspaceNotTheRow(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)
	waiting := model.(tui.Model).Init()

	model = key(model, "j")
	model = key(model, "j") // refactor-api, row index 2

	chosen, _ := model.(tui.Model).SelectedWorkspace()
	if chosen.Name != "refactor-api" {
		t.Fatalf("precondition failed, selected %q", chosen.Name)
	}

	// Remove a workspace above it. Row 2 is now a different workspace.
	if err := store.RemoveWorkspace("ws-feat-login"); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	model, _ = model.Update(nextMsg(t, waiting))

	after, ok := model.(tui.Model).SelectedWorkspace()
	if !ok {
		t.Fatal("nothing selected after the removal")
	}
	if after.Name != "refactor-api" {
		t.Errorf("selection drifted to %q after a workspace above it was removed", after.Name)
	}
}

// When the selected workspace itself disappears, the cursor has to land somewhere valid rather
// than pointing at nothing.
func TestSelectionRecoversWhenItsWorkspaceDisappears(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)
	waiting := model.(tui.Model).Init()

	if err := store.RemoveWorkspace("ws-feat-login"); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	model, _ = model.Update(nextMsg(t, waiting))

	got, ok := model.(tui.Model).SelectedWorkspace()
	if !ok {
		t.Fatal("nothing is selected after the selected workspace was removed")
	}
	if got.ID == "ws-feat-login" {
		t.Error("still pointing at the removed workspace")
	}
}

func TestEmptyStateExplainsWhatToDo(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, id := range []string{"ws-feat-login", "ws-fix-cache", "ws-refactor-api", "ws-spike-search"} {
		if err := store.RemoveWorkspace(id); err != nil {
			t.Fatalf("RemoveWorkspace(%s): %v", id, err)
		}
	}

	view := plain(tui.New(store).View())
	if !strings.Contains(view, "No worktrees found") {
		t.Errorf("the empty state should say so:\n%s", view)
	}
	if !strings.Contains(view, "git worktree add") {
		t.Errorf("the empty state should say what to do next:\n%s", view)
	}
}

// An untrusted project must say so on the dashboard, not only refuse quietly when something is
// run. Otherwise the first sign anything is wrong is a command that did not happen.
func TestUntrustedProjectIsVisible(t *testing.T) {
	store := fake.New()
	defer store.Close()

	store.SetTrust(core.TrustPending)

	view := plain(tui.New(store).View())
	if !strings.Contains(view, "not approved") {
		t.Errorf("an untrusted project should be visible in the header:\n%s", view)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		store := fake.New()

		var model tea.Model = tui.New(store)
		var msg tea.Msg
		switch k {
		case "q":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}

		_, cmd := model.Update(msg)
		if cmd == nil {
			t.Errorf("%s should quit", k)
		}
		store.Close()
	}
}

// The table has to survive an 80 column terminal with four or more workspaces, which is the P4-08
// criterion. Checked now because a layout that only works wide is expensive to narrow later.
func TestFitsIn80Columns(t *testing.T) {
	store := fake.New()
	defer store.Close()

	model := tui.New(store)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for i, line := range strings.Split(plain(next.View()), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line %d is %d columns wide, over the 80 column budget:\n%s",
				i, len([]rune(line)), line)
		}
	}
}
