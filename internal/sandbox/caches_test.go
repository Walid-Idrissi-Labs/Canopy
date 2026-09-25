package sandbox

import (
	"path/filepath"
	"slices"
	"testing"
)

// npx and uvx install the servers they start into caches the sandbox lets them write; the places a
// program would be run from, ~/.local/bin among them, stay closed.
func TestServerInstallersCanWriteTheirCachesAndNothingThatRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	policy := ForWorkspace(t.TempDir())
	for _, want := range []string{".npm/_npx", ".cache/uv", ".local/share/uv"} {
		if !slices.Contains(policy.Writable, filepath.Join(home, want)) {
			t.Errorf("%s is not writable", want)
		}
	}
	for _, closed := range []string{".local/bin", ".npm", ".cache"} {
		if slices.Contains(policy.Writable, filepath.Join(home, closed)) {
			t.Errorf("%s is writable", closed)
		}
	}
}
