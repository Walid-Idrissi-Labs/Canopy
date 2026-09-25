package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/lsp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/trust"
)

// check is one line of the doctor's report.
type check struct {
	level string // ok, note, warn or fail
	what  string
	say   string
}

// doctorEnv is what the doctor looks at, swapped in tests.
type doctorEnv struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
	gitLine  func() (string, error)
	gitTop   func() (string, error)
	dir      string
	keyStore func() (*keys.Store, error)
	sandbox  func() error
	terminal bool
}

func realDoctorEnv() doctorEnv {
	dir, _ := os.Getwd()
	return doctorEnv{
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		gitLine: func() (string, error) {
			out, err := exec.Command("git", "--version").Output()
			return strings.TrimSpace(string(out)), err
		},
		gitTop: func() (string, error) {
			out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
			return strings.TrimSpace(string(out)), err
		},
		dir:      dir,
		keyStore: openKeyStore,
		sandbox:  sandbox.Available,
		terminal: isTerminal(os.Stdout),
	}
}

// runDoctor says what is and is not in place, each with what to do about it, and exits 1 when
// something Canopy needs is missing.
func runDoctor(out io.Writer) int { return reportChecks(out, diagnose(realDoctorEnv())) }

// reportChecks prints the checks and chooses the exit code.
func reportChecks(out io.Writer, checks []check) int {
	failed := false
	for _, c := range checks {
		_, _ = fmt.Fprintf(out, "%-4s  %-18s %s\n", c.level, c.what, c.say)
		failed = failed || c.level == "fail"
	}
	if failed {
		return exitFailed
	}
	return exitOK
}

func diagnose(env doctorEnv) []check {
	var checks []check
	add := func(level, what, say string) { checks = append(checks, check{level, what, say}) }

	// git, which the worktrees, the review and landing all stand on.
	if line, err := env.gitLine(); err != nil {
		add("fail", "git", "not found; Canopy's worktrees, review and landing need it")
	} else {
		add("ok", "git", line)
	}
	if top, err := env.gitTop(); err != nil {
		add("note", "repository", "this directory is not in a git repository; agents need one to work in")
	} else if !sameDir(top, env.dir) {
		add("note", "repository", "inside "+top+"; canopy.json and agents' worktrees are read from here, "+
			"so run Canopy at the top to use the repository's own")
	}

	// Credentials.
	store, err := env.keyStore()
	switch {
	case err != nil:
		add("fail", "key store", fmt.Sprintf("%v. Where there is no keychain or Secret Service, "+
			"%s=file stores keys in a file only you can read", err, keys.BackendEnvVar))
	default:
		backend := store.BackendName()
		if err := store.Probe(); err != nil {
			add("fail", "key store", fmt.Sprintf("%s cannot be reached: %v. Where there is no keychain or "+
				"Secret Service, %s=file stores keys in a file only you can read", backend, err, keys.BackendEnvVar))
		} else if store.UsingInsecureBackend() {
			add("warn", "key store", backend+": keys are in a plain file, readable by anything running as you")
		} else {
			add("ok", "key store", backend)
		}
		stored, err := store.List()
		switch {
		case err != nil:
			add("fail", "keys", err.Error())
		case len(stored) > 0:
			add("ok", "keys", fmt.Sprintf("%d stored", len(stored)))
		default:
			found := ""
			for _, candidate := range importables() {
				if env.getenv(candidate.env) != "" {
					found = candidate.env
				}
			}
			if found != "" {
				add("warn", "keys", "none stored; `canopy keys import` stores "+found+" and the like as named keys")
			} else {
				add("warn", "keys", "none stored; `canopy keys add claude` stores one, `canopy keys signin` uses a subscription")
			}
		}
	}

	// The sandbox every shell command, test, hook and server runs in.
	if err := env.sandbox(); err != nil {
		add("warn", "sandbox", fmt.Sprintf("%v; commands run unconfined, as LIMITATIONS says", err))
	} else if strings.EqualFold(env.getenv("CANOPY_SANDBOX"), "off") {
		add("warn", "sandbox", "available but switched off with CANOPY_SANDBOX=off")
	} else {
		add("ok", "sandbox", "available")
	}

	// This project.
	project, found, err := config.Load(env.dir)
	switch {
	case err != nil:
		add("fail", "canopy.json", err.Error())
	case !found:
		if detected := config.DetectTests(env.dir); len(detected) > 0 {
			add("note", "canopy.json", "none; `canopy init` writes one with the "+count(len(detected), "test")+
				" this project's build files suggest")
		} else {
			add("note", "canopy.json", "none, so there are no tests to verify agents' work against")
		}
	default:
		req := trust.Describe(env.dir, project)
		trusted := req.Empty()
		if !trusted {
			if trustStore, err := trust.Open(); err == nil {
				trusted = trustStore.Trusted(req)
			}
		}
		switch {
		case !trusted:
			add("warn", "canopy.json", "not trusted, so none of it runs; `canopy trust` reviews it")
		case len(project.Tests) == 0:
			add("note", "canopy.json", "trusted, with no tests to verify agents' work against")
		default:
			add("ok", "canopy.json", "trusted, "+count(len(project.Tests), "test"))
		}
	}

	// Language servers, for diagnostics after edits and navigation.
	var have []string
	for _, name := range lsp.ServerNames() {
		if _, err := env.lookPath(name); err == nil {
			have = append(have, name)
		}
	}
	if len(have) > 0 {
		add("ok", "language servers", strings.Join(have, ", "))
	} else {
		add("note", "language servers", "none on PATH; with gopls, typescript-language-server, pyright or "+
			"rust-analyzer installed, agents see diagnostics after each edit")
	}

	// The programs the subscription routes delegate to.
	for _, route := range []struct{ what, program, say string }{
		{"claude route", "claude", "Claude Code, for signing in with a Claude subscription"},
		{"chatgpt route", "codex", "the Codex CLI, for signing in with ChatGPT"},
		{"copilot route", "copilot", "the Copilot CLI, for signing in with GitHub Copilot"},
	} {
		if _, err := env.lookPath(route.program); err == nil {
			add("ok", route.what, route.program+" found")
		} else {
			add("note", route.what, route.program+" not found; "+route.say)
		}
	}

	// The bridge, found the way the Claude route finds it: either name, or CANOPY_CLAUDE_ACP.
	if bridge, err := (acp.Discovery{LookPath: env.lookPath, Getenv: env.getenv}).Bridge(); err == nil {
		add("ok", "claude bridge", bridge+" found")
	} else {
		add("note", "claude bridge", "not found; the ACP bridge the Claude route talks through")
	}

	// The terminal.
	switch {
	case !env.terminal:
		add("note", "terminal", "output is not a terminal, so the interface cannot open here")
	case env.getenv("NO_COLOR") != "":
		add("ok", "terminal", "NO_COLOR is set, so the interface is drawn without colour")
	case strings.Contains(env.getenv("COLORTERM"), "truecolor") || strings.Contains(env.getenv("COLORTERM"), "24bit"):
		add("ok", "terminal", "true colour")
	default:
		add("note", "terminal", "no COLORTERM=truecolor, so colours are rounded to the terminal's palette")
	}
	if env.getenv("TMUX") != "" {
		add("note", "tmux", "copying to the clipboard needs `set -g set-clipboard on` in tmux")
	}
	return checks
}

// count says n of a thing, in the singular for one.
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
