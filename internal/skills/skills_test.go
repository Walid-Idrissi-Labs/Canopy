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

// A SKILL.md replaced by a symlink after loading is refused at read time.
func TestASkillReplacedByASymlinkIsNotRead(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	dir := writeSkill(t, filepath.Join(project, ".claude", "skills"), "x", "d", "body")
	set := Load(project, true)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if body, err := set.Body("x", ""); err == nil || strings.Contains(body, "SECRET") {
		t.Fatalf("a symlinked SKILL.md was read: %q %v", body, err)
	}
}

func TestAFoldedDescriptionIsRead(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	dir := filepath.Join(project, ".claude", "skills", "f")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: f\ndescription: >\n  cut a release\n  and tag it\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(project, true).Listing(); !strings.Contains(got, "f: cut a release and tag it") {
		t.Fatalf("listing = %q", got)
	}
}

// The whole skill folder swapped for a link to somewhere else after loading leads nowhere: the
// folder a file must sit in is fixed when the skill is loaded, not looked up again at read time.
func TestASkillFolderSwappedForALinkIsNotRead(t *testing.T) {
	isolate(t)
	project := t.TempDir()
	dir := writeSkill(t, filepath.Join(project, ".claude", "skills"), "x", "d", "body")
	set := Load(project, true)
	outside := writeSkill(t, t.TempDir(), "x", "d", "body")
	if err := os.WriteFile(filepath.Join(outside, "credentials"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"", "credentials"} {
		if body, err := set.Body("x", file); err == nil || strings.Contains(body, "SECRET") {
			t.Fatalf("%q was read through a swapped folder: %q %v", file, body, err)
		}
	}
}
