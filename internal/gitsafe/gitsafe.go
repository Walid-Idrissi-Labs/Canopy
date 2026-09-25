// Package gitsafe is how Canopy runs git without letting the repository run anything.
//
// Git reads configuration from the repository it is pointed at, and several settings in that
// configuration are commands: core.fsmonitor runs on every status, core.hooksPath and the hooks
// directory run on commit, ext:: remotes run on fetch. Canopy calls git on its own, every two
// seconds from the revision poller among other places, so any of those settings in a repository's
// .git/config is code that runs with nobody having approved it. This package is the one place that
// switches them off.
package gitsafe

import (
	"fmt"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// overrides are applied as command line configuration through the environment, which git gives
// the highest precedence below an explicit -c. The hooks path points at the null device: git looks
// for hooks there, finds none, and runs none.
var overrides = [][2]string{
	{"core.fsmonitor", "false"},
	{"core.hooksPath", os.DevNull},
	{"protocol.ext.allow", "never"},
}

// Env returns base with git's configuration hardened. Any GIT_CONFIG_COUNT style variables already
// in base are dropped first, since a count left over from the user's shell would otherwise decide
// which of these git reads.
func Env(base []string) []string { return envWith(base, overrides) }

// EnvFor is Env for git run in dir, which additionally neutralises the filters that repository's own
// configuration defines. git status runs a clean filter while comparing file contents, and a
// .gitattributes line can attach one that .git/config defines, which is a command every two seconds.
// Only filters defined in the repository's local or worktree scope are replaced: the user's global
// ones, Git LFS above all, keep working, or every checkpoint would store raw LFS content.
func EnvFor(dir string, base []string) []string {
	return envWith(base, append(append([][2]string(nil), overrides...), localFilters(dir)...))
}

// InheritedFor is EnvFor applied to the current environment with secrets removed.
func InheritedFor(dir string) []string { return EnvFor(dir, childenv.Inherited()) }

type cached struct {
	at      time.Time
	filters [][2]string
}

var (
	filterMu    sync.Mutex
	filterCache = map[string]cached{}
)

// localFilters lists overrides for every filter driver configured in dir's local or worktree
// config. Reading configuration runs nothing. Cached briefly, because the revision poller calls git
// in the same directory every two seconds.
func localFilters(dir string) [][2]string {
	if dir == "" {
		return nil
	}
	filterMu.Lock()
	if c, ok := filterCache[dir]; ok && time.Since(c.at) < 5*time.Second {
		filterMu.Unlock()
		return c.filters
	}
	filterMu.Unlock()

	cmd := exec.Command("git", "config", "--show-scope", "--get-regexp", `^filter\.`)
	cmd.Dir = dir
	cmd.Env = Env(childenv.Inherited())
	out, _ := cmd.Output()
	seen := map[string]bool{}
	var filters [][2]string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "local" && fields[0] != "worktree") {
			continue
		}
		key := fields[1]
		dot := strings.LastIndex(key, ".")
		if dot <= len("filter.") {
			continue
		}
		name := key[len("filter."):dot]
		if seen[name] {
			continue
		}
		seen[name] = true
		filters = append(filters,
			[2]string{"filter." + name + ".clean", "cat"},
			[2]string{"filter." + name + ".smudge", "cat"},
			[2]string{"filter." + name + ".process", ""})
	}
	filterMu.Lock()
	filterCache[dir] = cached{at: time.Now(), filters: filters}
	filterMu.Unlock()
	return filters
}

func envWith(base []string, overrides [][2]string) []string {
	out := make([]string, 0, len(base)+2*len(overrides)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") ||
			strings.HasPrefix(kv, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(kv, "GIT_CONFIG_VALUE_") ||
			strings.HasPrefix(kv, "GIT_CONFIG_PARAMETERS=") ||
			strings.HasPrefix(kv, "GIT_EXTERNAL_DIFF=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_CONFIG_COUNT="+strconv.Itoa(len(overrides)))
	for i, kv := range overrides {
		n := strconv.Itoa(i)
		out = append(out, "GIT_CONFIG_KEY_"+n+"="+kv[0], "GIT_CONFIG_VALUE_"+n+"="+kv[1])
	}
	return out
}

// Inherited is Env applied to the current process environment, for git invocations that need the
// user's identity and credentials but must still not execute repository configuration.
func Inherited() []string { return Env(childenv.Inherited()) }

// CheckVersion reports a problem when the installed git is too old for these overrides to hold.
// Configuration through the environment arrived in git 2.31; an older git
// ignores them without complaint, which would leave every protection here silently off.
func CheckVersion() error {
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return fmt.Errorf("git could not be run: %w", err)
	}
	var major, minor int
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return fmt.Errorf("could not read the git version from %q", strings.TrimSpace(string(out)))
	}
	if _, err := fmt.Sscanf(fields[2], "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("could not read the git version from %q", strings.TrimSpace(string(out)))
	}
	if major < 2 || (major == 2 && minor < 31) {
		return fmt.Errorf("git %d.%d is older than 2.31, so a repository's own git configuration "+
			"(filters, fsmonitor, hooks) can run commands when Canopy calls git; upgrade git", major, minor)
	}
	return nil
}
