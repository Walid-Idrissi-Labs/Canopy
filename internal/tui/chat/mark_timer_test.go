package chat

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestACompletedConversationStopsTheMarkTimer(t *testing.T) {
	m := Model{
		loaded:      true,
		markRunning: true,
		session: core.Session{Turns: []core.Turn{{
			State: core.TurnComplete,
		}}},
	}

	next, command := m.Update(markTickMsg{generation: m.markGeneration})
	if command != nil {
		t.Fatal("a static completed conversation scheduled another mark tick")
	}
	if next.markRunning {
		t.Fatal("the model still records a mark timer after consuming its last tick")
	}
	if next.markStep != 0 {
		t.Errorf("the invisible mark advanced to %d", next.markStep)
	}
}

func TestStartingWorkRestartsExactlyOneMarkTimer(t *testing.T) {
	m := Model{
		loaded: true,
		session: core.Session{Turns: []core.Turn{{
			State: core.TurnComplete,
		}}},
		working: true,
	}

	if command := m.ensureMark(); command == nil {
		t.Fatal("starting work did not restart the mark")
	}
	if !m.markRunning {
		t.Fatal("the restarted mark was not recorded")
	}
	if command := m.ensureMark(); command != nil {
		t.Fatal("a second observer scheduled a duplicate mark timer")
	}
}
