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
	// DenyWrite are trees inside writable directories that still may not be written, where the
	// platform can say so.
	DenyWrite []string
	// DenyWriteExact are single paths, a directory entry or a file, that may not be written,
	// renamed or removed, while what is inside a directory stays as writable as it was.
	DenyWriteExact []string
	// Devices are device files that may be written: null, tty and the like, and not the whole of
	// /dev, which holds other terminals.
	Devices []string
	Network Network
}

// ErrUnavailable means this platform or machine cannot confine a command.
var ErrUnavailable = errors.New("no sandbox is available here")

// DisableEnvVar turns sandboxing off when set to "off", for the case where it breaks a workflow
// and somebody decides the prompt is protection enough.
const DisableEnvVar = "CANOPY_SANDBOX"

// TrampolineArg is the first argument Canopy recognises as "confine yourself, then run this", on
// platforms that confine by re-running Canopy.
const TrampolineArg = "__canopy-sandbox"

// Disabled reports whether the user switched sandboxing off.
func Disabled() bool { return strings.EqualFold(os.Getenv(DisableEnvVar), "off") }

// ForWorkspace is the default policy for commands an agent runs in workspace: write the workspace,
// any extra directories given (a worktree's shared git directory, for instance), temporary
// directories and the download caches toolchains keep; never read credentials; never write a git
// directory's hooks or config, which later git commands run.
//
// Caches only, and not the toolchain homes around them: ~/go/bin, ~/.cargo/bin and the like are on
// PATH and ~/.gradle/init.d runs on every build, so a writable home is a way to leave a program for
// the user's next ordinary command to run outside the sandbox.
func ForWorkspace(workspace string, extra ...string) Policy {
	home, _ := os.UserHomeDir()
	writable := append([]string{workspace}, extra...)
	// The per-user temporary area, which on macOS is a directory holding both T (temporary) and C
	// (caches) that toolchains write into.
	temp := filepath.Clean(os.TempDir())
	if filepath.Base(temp) == "T" {
		temp = filepath.Dir(temp)
	}
	writable = append(writable, temp, "/tmp", "/private/tmp", "/dev/fd")
	if home != "" {
		for _, rel := range []string{
			".cache/go-build", ".cache/pip", ".cache/yarn", ".cache/pnpm", ".cache/deno",
			"Library/Caches/go-build", "Library/Caches/pip", "Library/Caches/Yarn", "Library/Caches/pnpm",
			"Library/Caches/deno", "go/pkg/mod", ".npm/_cacache", ".npm/_logs", ".pnpm-store",
			".yarn/berry/cache", ".cargo/registry", ".cargo/git", ".gradle/caches",
			".m2/repository", ".nuget/packages", ".bun/install/cache", ".pub-cache/hosted",
			"Library/Developer/Xcode/DerivedData",
		} {
			writable = append(writable, filepath.Join(home, rel))
		}
	}
	for _, env := range []string{"GOCACHE", "GOMODCACHE", "npm_config_cache", "PIP_CACHE_DIR"} {
		if v := os.Getenv(env); v != "" {
			writable = append(writable, v)
		}
	}
	var deny []string
	if home != "" {
		for _, rel := range []string{
			".ssh", ".aws", ".gnupg", ".azure", ".kube", ".docker/config.json", ".netrc", ".git-credentials",
			".config/gh", ".config/gcloud", ".config/canopy", ".config/op", "Library/Keychains",
			"Library/Application Support/canopy", "Library/Application Support/Google/Chrome",
			"Library/Application Support/Firefox", "Library/Cookies", ".password-store",
			".npmrc", ".pypirc", ".cargo/credentials", ".cargo/credentials.toml", ".gem/credentials",
			".m2/settings.xml", ".gradle/gradle.properties", ".terraform.d/credentials.tfrc.json",
			".vault-token",
		} {
			deny = append(deny, filepath.Join(home, rel))
		}
	}
	return Policy{Writable: clean(writable), DenyRead: clean(deny), Network: NetworkOpen,
		Devices: []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/tty"}}
}

// WithGitDirs forbids writing the hooks and config of the given git directories, which git runs or
// obeys on the user's next command. Enforced where the platform can carve a path out of a writable
// tree (macOS); recorded otherwise.
//
// The git directory's own entry, and a worktree's gitdir and commondir files, are denied as well:
// otherwise a command renames .git out of the way, writes hooks and config in the renamed copy, and
// renames it back, or points a worktree's .git file somewhere it controls.
func (p Policy) WithGitDirs(dirs ...string) Policy {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p.DenyWrite = append(p.DenyWrite, filepath.Join(d, "hooks"), filepath.Join(d, "config"))
		p.DenyWriteExact = append(p.DenyWriteExact, d,
			filepath.Join(d, "commondir"), filepath.Join(d, "gitdir"))
	}
	p.DenyWrite = clean(p.DenyWrite)
	p.DenyWriteExact = clean(p.DenyWriteExact)
	return p
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
