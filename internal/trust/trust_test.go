package trust

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
)

// A delegated vendor agent applies the repository's own settings, hooks included, outside Canopy's
// gate, so a directory carrying them must have been trusted before one starts there.
func TestDelegationNeedsTrustOnlyWhereVendorSettingsExist(t *testing.T) {
	t.Setenv("CANOPY_TRUST_FILE", filepath.Join(t.TempDir(), "trust.json"))
	dir := t.TempDir()
	if err := DelegationAllowed(dir); err != nil {
		t.Fatalf("a directory with no vendor settings was refused: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DelegationAllowed(dir); !errors.Is(err, ErrVendorSettingsUntrusted) {
		t.Fatalf("untrusted vendor settings were allowed: %v", err)
	}
	store, _ := Open()
	if err := store.Grant(Describe(dir, config.Project{})); err != nil {
		t.Fatal(err)
	}
	if err := DelegationAllowed(dir); err != nil {
		t.Fatalf("trusted vendor settings were refused: %v", err)
	}
}
