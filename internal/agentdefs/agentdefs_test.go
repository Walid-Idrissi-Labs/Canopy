package agentdefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !ok || got.Model != "sonnet" || !strings.Contains(got.Body, "Report bugs only") {
		t.Fatalf("definition = %+v", got)
	}
	if !strings.Contains(set.Listing(), "reviewer: reviews diffs") {
		t.Fatalf("listing = %q", set.Listing())
	}
}
