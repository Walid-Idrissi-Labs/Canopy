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
	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
	"os"
	"strconv"
	"strings"
)

// overrides are applied as command line configuration through the environment, which git gives
// the highest precedence below an explicit -c. The hooks path points at the null device: git looks
// for hooks there, finds none, and runs none.
var overrides = [][2]string{
	{"core.fsmonitor", "false"},
	{"core.hooksPath", os.DevNull},
	{"protocol.ext.allow", "never"},
	{"core.sshCommand", "ssh"},
}

// Env returns base with git's configuration hardened. Any GIT_CONFIG_COUNT style variables already
// in base are dropped first, since a count left over from the user's shell would otherwise decide
// which of these git reads.
func Env(base []string) []string {
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
