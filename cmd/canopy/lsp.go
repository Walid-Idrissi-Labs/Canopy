package main

import (
	"os"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/lsp"
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
// agent's worktree has servers of its own that see its files.
func attachLanguageServers(w *tools.Workspace) {
	languageServers.Lock()
	defer languageServers.Unlock()
	if !languageServers.enabled {
		return
	}
	if languageServers.managers == nil {
		languageServers.managers = map[string]*lsp.Manager{}
	}
	m, ok := languageServers.managers[w.Root()]
	if !ok {
		m = lsp.NewManager(w.Root())
		languageServers.managers[w.Root()] = m
	}
	w.SetDiagnoser(m)
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
