package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/lsp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
)

// Language servers check what the edit and write tools change. They run the repository's own
// toolchain outside the sandbox, so only in a trusted repository, and CANOPY_LSP=off turns them off.
var languageServers struct {
	sync.Mutex
	enabled  bool
	managers map[string]*lsp.Manager
}

// enableLanguageServers decides, once the project's trust is known, whether workspaces get them.
func enableLanguageServers(project config.Project) {
	languageServers.Lock()
	defer languageServers.Unlock()
	languageServers.enabled = project.Trusted && !strings.EqualFold(os.Getenv("CANOPY_LSP"), "off")
}

// attachLanguageServers gives a workspace a diagnoser, one manager per workspace root, so each
// agent's worktree has servers of its own that see its files, and returns the navigation tools
// that ask the same servers. Nothing when they are off.
func attachLanguageServers(w *tools.Workspace) []core.Tool {
	languageServers.Lock()
	defer languageServers.Unlock()
	if !languageServers.enabled {
		return nil
	}
	if languageServers.managers == nil {
		languageServers.managers = map[string]*lsp.Manager{}
	}
	m, ok := languageServers.managers[w.Root()]
	if !ok {
		m = lsp.NewManager(w.Root())
		m.SetWrap(func(argv []string) ([]string, error) { return confineServer(w, argv) })
		languageServers.managers[w.Root()] = m
	}
	w.SetDiagnoser(m)
	return tools.NavigationTools(w, m)
}

// closeLanguageServers stops every server started.
func closeLanguageServers() {
	languageServers.Lock()
	managers := languageServers.managers
	languageServers.managers = nil
	languageServers.Unlock()
	for _, m := range managers {
		m.Close()
	}
}

// confineServer runs a language server inside the workspace's sandbox, with its own cache writable:
// it reads files the sandboxed agent writes, and a server that ran unconfined would carry what the
// agent wrote out of the sandbox. Where there is no sandbox there is no server, unless the sandbox
// was switched off on purpose with CANOPY_SANDBOX=off.
func confineServer(w *tools.Workspace, argv []string) ([]string, error) {
	if sandbox.Disabled() {
		return argv, nil
	}
	policy := w.SandboxPolicy()
	if cache, err := os.UserCacheDir(); err == nil {
		for _, dir := range []string{"gopls", "typescript", "clangd", "pyright", "basedpyright", "rust-analyzer"} {
			policy.Writable = append(policy.Writable, filepath.Join(cache, dir))
		}
	}
	name, args, err := policy.Wrap(argv[0], argv[1:])
	if err != nil {
		return nil, err
	}
	return append([]string{name}, args...), nil
}
