package sandbox

import (
	"path/filepath"
	"slices"
	"testing"
)

// What npx and uv install and later run from is not writable: the user's own next npx or uv tool
// run would use whatever a sandboxed command left there, outside the sandbox. Servers get
// installers' directories of their own instead (see mcpInstallDirs).
func TestInstallersDirectoriesThatRunLaterStayClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UV_CACHE_DIR", filepath.Join(home, "uvcache"))
	policy := ForWorkspace(t.TempDir())
	for _, closed := range []string{".local/bin", ".local/share/uv", ".npm/_npx", ".npm", ".cache", ".cache/uv", "uvcache"} {
		if slices.Contains(policy.Writable, filepath.Join(home, closed)) {
			t.Errorf("%s is writable", closed)
		}
	}
}
