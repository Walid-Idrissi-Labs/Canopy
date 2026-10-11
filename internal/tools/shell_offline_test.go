package tools

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// A command is offline only with the sandbox on, able to limit the network, and the network set
// to off; anything less leaves it able to reach out, whatever it runs.
func TestAShellCommandIsOfflineOnlyWhenTheNetworkIsCut(t *testing.T) {
	if sandbox.Available() != nil || !sandbox.NetworkSealed() {
		// Linux included: Landlock leaves UDP and name lookups open, so nothing there is offline.
		w, err := OpenWorkspace(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("CANOPY_SANDBOX_NETWORK", "off")
		if ShellTool(w).(interface{ Offline() bool }).Offline() {
			t.Fatal("a sandbox that cannot seal the network reads as offline")
		}
		return
	}
	w, err := OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shell := ShellTool(w).(interface{ Offline() bool })
	t.Setenv("CANOPY_SANDBOX_NETWORK", "off")
	t.Setenv("CANOPY_SANDBOX", "")
	if !shell.Offline() {
		t.Fatal("the network set to off is not offline")
	}
	for _, setting := range [][2]string{{"CANOPY_SANDBOX_NETWORK", "open"}, {"CANOPY_SANDBOX_NETWORK", "registries"}} {
		t.Setenv(setting[0], setting[1])
		if shell.Offline() {
			t.Errorf("%s=%s reads as offline", setting[0], setting[1])
		}
	}
	t.Setenv("CANOPY_SANDBOX_NETWORK", "off")
	t.Setenv("CANOPY_SANDBOX", "off")
	if shell.Offline() {
		t.Error("the sandbox switched off reads as offline")
	}
}
