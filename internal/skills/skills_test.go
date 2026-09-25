package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, desc, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	return home
}

// Only names and descriptions go in the listing; the body arrives through the tool.
func TestSkillsAreListedShortAndLoadedOnDemand(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	dir := writeSkill(t, filepath.Join(project, ".claude", "skills"), "release", "cut a release", "Step one: tag it.")
	if err := os.WriteFile(filepath.Join(dir, "checklist.md"), []byte("- notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	set := Load(project, true)
	listing := set.Listing()
	if !strings.Contains(listing, "release: cut a release") || strings.Contains(listing, "Step one") {
		t.Fatalf("listing = %q", listing)
	}
	body, err := set.Body("release", "")
	if err != nil || !strings.Contains(body, "Step one") || !strings.Contains(body, "checklist.md") {
		t.Fatalf("body = %q, %v", body, err)
	}
	if extra, err := set.Body("release", "checklist.md"); err != nil || extra != "- notes" {
		t.Fatalf("file = %q, %v", extra, err)
	}
}

// A repository's skills are instructions from the repository, so they only load when it is trusted,
// and a skill cannot hand out files from outside its own folder.
func TestProjectSkillsNeedTrustAndStayInTheirFolder(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "sneaky", "x", "body")
	if !Load(project, false).Empty() {
		t.Fatal("a project skill loaded without trust")
	}
	set := Load(project, true)
	for _, file := range []string{"../../../../etc/hosts", "/etc/hosts"} {
		if _, err := set.Body("sneaky", file); err == nil {
			t.Errorf("%s was read through a skill", file)
		}
	}
}
