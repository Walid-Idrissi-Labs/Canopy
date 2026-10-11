package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// A local server started with a sandbox cannot write outside its workspace, and still serves.
func TestAConfinedServerCannotWriteOutsideItsWorkspace(t *testing.T) {
	if err := sandbox.Available(); err != nil {
		if os.Getenv("CANOPY_REQUIRE_SANDBOX") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	// Outside everything the sandbox lets a command write: the user's cache directory is not, only
	// some toolchains' folders inside it are. Wherever the checkout is, this is outside.
	workspace := t.TempDir()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	outside, err := os.MkdirTemp(cache, "canopy-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	if resolved, err := filepath.EvalSymlinks(outside); err == nil {
		outside = resolved
	}
	target := filepath.Join(outside, "escaped.txt")

	spec := serverSpec("confined", "normal")
	spec.Dir = workspace
	spec.Env = append(spec.Env, writeToEnv+"="+target)
	policy := sandbox.ForWorkspace(workspace)
	spec.Sandbox = &policy
	var session *Session
	session, err = Connect(context.Background(), spec)
	if err != nil {
		t.Fatalf("a confined server did not start: %v", err)
	}
	session.Close()
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a confined server wrote outside its workspace")
	}

	// The same server unconfined can, which is what makes the check above mean something.
	spec.Sandbox = nil
	session, err = Connect(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	if _, err := os.Stat(target); err != nil {
		t.Fatal("the unconfined server could not write either, so the test proves nothing")
	}
}
