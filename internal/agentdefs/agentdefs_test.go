package agentdefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestDefinitionsLoadAndNeedTrust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	project := t.TempDir()
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := "---\nname: reviewer\ndescription: reviews diffs\nmodel: sonnet\n---\nRead the diff. Report bugs only.\n"
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(project, false).Get("reviewer"); ok {
		t.Fatal("a project definition loaded without trust")
	}
	set := Load(project, true)
	got, ok := set.Get("reviewer")
	if !ok || got.Model != "" || !strings.Contains(got.Body, "Report bugs only") {
		t.Fatalf("definition = %+v", got)
	}
	if !strings.Contains(set.Listing(), "reviewer: reviews diffs") {
		t.Fatalf("listing = %q", set.Listing())
	}
}

// A Claude Code tools list becomes a trust ceiling, in either YAML form, so a read-only reviewer
// does not run with an editor and a shell; a model named by family alone is left to the session.
func TestAToolsListCapsTrust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	project := t.TempDir()
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.md": "---\nname: a\ntools: Read, Grep, Glob\nmodel: opus\n---\nbody\n",
		"b.md": "---\nname: b\ntools:\n  - Read\n  - Edit\nmodel: claude-sonnet-5\n---\nbody\n",
		"c.md": "---\nname: c\ntools: [Read, Bash]\n---\nbody\n",
		"d.md": "---\nname: d\n---\nbody\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set := Load(project, true)
	for name, want := range map[string]core.TrustLevel{"a": core.TrustReadOnly, "b": core.TrustConfined,
		"c": core.TrustStandard, "d": ""} {
		if got, _ := set.Get(name); got.Ceiling != want {
			t.Errorf("%s: ceiling %q, want %q", name, got.Ceiling, want)
		}
	}
	if a, _ := set.Get("a"); a.Model != "" {
		t.Errorf("a family name became a model: %q", a.Model)
	}
	if b, _ := set.Get("b"); b.Model != "claude-sonnet-5" || b.Body != "body" {
		t.Errorf("b = %+v", b)
	}
}
