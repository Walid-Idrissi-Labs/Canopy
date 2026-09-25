package session

import (
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

// tainted reports whether a conversation has taken in content from outside: a page fetched, a web
// search the provider ran, or any MCP tool's result. Such content can carry instructions nobody
// here wrote, and the permission layer then asks about anything that could send data out.
//
// Worked out from the conversation's own record, so it holds across compaction and a restart, and
// sticky once true. A dispatched agent inherits it from the conversation that started it.
func (e *Engine) tainted(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taint[sessionID] {
		return true
	}
	session, ok := e.sessions[sessionID]
	if !ok {
		return false
	}
	tools, _ := e.toolsForLocked(sessionID)
	for _, turn := range session.Turns {
		for _, notice := range turn.Notices {
			if strings.HasPrefix(notice, anthropic.WebSearchNotice) {
				return e.markTaintedLocked(sessionID)
			}
		}
		for _, call := range turn.ToolCalls {
			if taintSource(tools, call.Name) {
				return e.markTaintedLocked(sessionID)
			}
		}
	}
	return false
}

func (e *Engine) markTaintedLocked(sessionID string) bool {
	if e.taint == nil {
		e.taint = map[string]bool{}
	}
	e.taint[sessionID] = true
	return true
}

// taintSource reports whether a tool brings outside content in: anything on the network, and any
// tool reached over MCP, whose results are another program's to write.
func taintSource(tools *core.ToolRegistry, name string) bool {
	if tools == nil {
		return false
	}
	tool, ok := tools.Get(name)
	if !ok {
		return false
	}
	if external, ok := tool.(core.ExternalTool); ok && external.External() {
		return true
	}
	return tool.Kind() == core.ToolNetwork
}
