package session

import (
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

// tainted reports whether a conversation has taken in content from outside: a page fetched, a web
// search the provider ran, or any MCP tool's result. Such content can carry instructions nobody
// here wrote, and the permission layer then asks about anything that could send data out (D-57).
//
// Worked out from the conversation's own record and saved once found, so it holds across
// compaction, a restart and a pickup. Agents a tainted conversation starts inherit it, and a
// tainted agent passes it to the conversation it reports to.
func (e *Engine) tainted(sessionID string) bool {
	e.mu.Lock()
	if e.taint[sessionID] {
		e.mu.Unlock()
		return true
	}
	found := false
	if session, ok := e.sessions[sessionID]; ok {
		tools, _ := e.toolsForLocked(sessionID)
		found = recordTainted(session, tools)
	}
	e.mu.Unlock()
	if found {
		e.markTainted(sessionID)
	}
	return found
}

// recordTainted reads a conversation's turns for outside content.
func recordTainted(session *core.Session, tools *core.ToolRegistry) bool {
	for _, turn := range session.Turns {
		for _, notice := range turn.Notices {
			if strings.HasPrefix(notice, anthropic.WebSearchNotice) {
				return true
			}
		}
		// Only calls that have come back: the call being decided on has brought nothing in yet.
		answered := map[string]bool{}
		for _, result := range turn.ToolResults {
			answered[result.CallID] = true
		}
		for _, call := range turn.ToolCalls {
			if answered[call.ID] && taintSource(tools, call.Name) {
				return true
			}
		}
	}
	return false
}

// markTainted records the taint in memory and in the conversation's saved record.
func (e *Engine) markTainted(sessionID string) {
	e.mu.Lock()
	if e.taint == nil {
		e.taint = map[string]bool{}
	}
	fresh := !e.taint[sessionID]
	e.taint[sessionID] = true
	e.mu.Unlock()
	if fresh {
		e.persist(func(s *Storage) error { return s.SaveSessionTaint(sessionID) })
	}
}

// taintParent passes a child's taint to the conversation it reports to: the report carries what the
// child read, and the parent acts on it.
func (e *Engine) taintParent(child, parent string) {
	if parent != "" && e.tainted(child) {
		e.markTainted(parent)
	}
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
