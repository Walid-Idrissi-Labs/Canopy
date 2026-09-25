// Package sandbox confines the commands Canopy runs for an agent.
//
// A command a model wrote runs with the user's account. Before this package, the only thing between
// it and the rest of the machine was the permission prompt, and in modes that do not prompt, not
// even that. A sandboxed command can write only inside its workspace, the temporary directories and
// the build caches a toolchain needs, and cannot read the places credentials live. What it covers
// differs by platform, and each platform says so rather than implying more:
//
//   - macOS: a Seatbelt profile through sandbox-exec. Writes are confined, the listed credential
//     paths cannot be read, and network access can be switched off.
//   - Linux: Landlock, applied by re-running Canopy as a small trampoline that restricts itself and
//     then executes the command. Writes are confined; reads are not restricted, because Landlock
//     cannot exclude a directory from a readable tree; TCP connections can be switched off on
//     kernels with Landlock ABI 4 or later.
//   - Elsewhere, nothing, and Available says why.
package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Network is what a sandboxed command may reach.
type Network string

const (
	// NetworkOpen leaves the network alone: package managers and fetches work.
	NetworkOpen Network = "open"
	// NetworkNone refuses outbound connections.
	NetworkNone Network = "none"
)

// Policy is one command's confinement.
type Policy struct {
	// Writable are the directories the command may create, change and delete files beneath.
	Writable []string
	// DenyRead are paths the command may not read at all, where the platform can say so.
	DenyRead []string
	Network  Network
}

// ErrUnavailable means this platform or machine cannot confine a command.
var ErrUnavailable = errors.New("no sandbox is available here")

// DisableEnvVar turns sandboxing off when set to "off", for the case where it breaks a workflow
// and somebody decides the prompt is protection enough.
const DisableEnvVar = "CANOPY_SANDBOX"

// Disabled reports whether the user switched sandboxing off.
func Disabled() bool { return strings.EqualFold(os.Getenv(DisableEnvVar), "off") }

// ForWorkspace is the default policy for commands an agent runs in workspace: write the workspace,
// any extra directories given (a worktree's shared git directory, for instance), temporary
// directories and toolchain caches; never read credentials.
func ForWorkspace(workspace string, extra ...string) Policy {
	home, _ := os.UserHomeDir()
	writable := append([]string{workspace}, extra...)
	writable = append(writable, os.TempDir(), "/tmp", "/private/tmp", "/private/var/folders", "/dev")
	if home != "" {
		for _, rel := range []string{
			".cache", "Library/Caches", "go", ".npm", ".yarn", ".pnpm-store", ".cargo", ".rustup",
			".gradle", ".m2", ".local/share/pnpm", ".bun", ".deno", ".nuget", ".pub-cache",
			"Library/pnpm", ".config/yarn",
		} {
			writable = append(writable, filepath.Join(home, rel))
		}
	}
	for _, env := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "npm_config_cache", "CARGO_HOME", "PIP_CACHE_DIR"} {
		if v := os.Getenv(env); v != "" {
			writable = append(writable, v)
		}
	}
	var deny []string
	if home != "" {
		for _, rel := range []string{
			".ssh", ".aws", ".gnupg", ".azure", ".kube", ".docker/config.json", ".netrc", ".git-credentials",
			".config/gh", ".config/gcloud", ".config/canopy", "Library/Keychains",
			"Library/Application Support/canopy", "Library/Application Support/Google/Chrome",
			"Library/Application Support/Firefox", "Library/Cookies", ".password-store",
		} {
			deny = append(deny, filepath.Join(home, rel))
		}
	}
	return Policy{Writable: clean(writable), DenyRead: clean(deny), Network: NetworkOpen}
}

func clean(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
