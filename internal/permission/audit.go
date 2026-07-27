package permission

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The audit trail exists to answer one question after the fact: what did this agent actually do.
//
// Not "what was it allowed to do", which is the trust level, and not "what did it say it did",
// which is the transcript and which a model can be wrong about in good faith. Every call that
// reached the permission layer is recorded with what was asked, what was decided, and what came
// back, whether it ran or not.
//
// The refused calls matter as much as the successful ones. An agent that tried to write outside its
// workspace nine times and was stopped nine times is a very different thing from one that never
// tried, and only the trail can tell them apart.

// Entry is one recorded tool call.
type Entry struct {
	At time.Time

	AgentID   string
	SessionID string

	Tool string
	Kind core.ToolKind

	// Arguments is what the model asked for, as it asked for it.
	//
	// Kept verbatim rather than summarised, because the question this is here to answer is usually
	// about a specific call and a summary is exactly the detail that got dropped. Bounded, because
	// a write tool's arguments contain the whole file.
	Arguments string

	Outcome Outcome
	// Reason is why it was decided that way.
	Reason string

	// Ran says whether the tool actually executed. An allowed call that then failed to run is
	// different from one that was refused, and both are different from one that ran and failed.
	Ran bool
	// Result is what came back, bounded the same way arguments are.
	Result string
	// Failed says the tool ran and reported a failure.
	Failed bool

	Duration time.Duration
}

// Summary is the one line form, for a list.
func (e Entry) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", e.At.Format("15:04:05"), e.Tool)

	switch {
	case e.Outcome == Deny:
		b.WriteString(" refused")
	case !e.Ran:
		b.WriteString(" not run")
	case e.Failed:
		b.WriteString(" failed")
	default:
		b.WriteString(" ok")
	}

	if e.Duration > 0 {
		fmt.Fprintf(&b, " in %s", e.Duration.Round(time.Millisecond))
	}
	return b.String()
}

// maxRecorded bounds what is kept per entry.
//
// A write tool's arguments are a whole file and a shell tool's result can be a build log. Keeping
// them whole would make the trail larger than the work it describes, and the part somebody needs
// when reading it back is the beginning.
const maxRecorded = 2000

func bound(s string) string {
	if len(s) <= maxRecorded {
		return s
	}
	return s[:maxRecorded] + fmt.Sprintf("\n... %d more bytes ...", len(s)-maxRecorded)
}

// Trail is the audit log.
//
// In memory, with a cap. Persisting it belongs with the session storage and is a task of its own;
// what matters now is that the record exists and that nothing can run without producing one.
type Trail struct {
	mu      sync.RWMutex
	entries []Entry
	limit   int
}

// DefaultTrailLimit is how many calls are kept.
//
// Generous, because the value of a trail is in being able to look back further than you expected to
// need to, and an entry is small once the arguments are bounded.
const DefaultTrailLimit = 5000

// NewTrail builds an audit trail.
func NewTrail() *Trail { return &Trail{limit: DefaultTrailLimit} }

// Record adds an entry.
func (t *Trail) Record(entry Entry) {
	entry.Arguments = bound(entry.Arguments)
	entry.Result = bound(entry.Result)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.entries = append(t.entries, entry)
	if len(t.entries) > t.limit {
		// The oldest go first. A trail that dropped the newest would be one that stops recording
		// exactly when an agent is at its busiest, which is when you need it.
		t.entries = t.entries[len(t.entries)-t.limit:]
	}
}

// Entries returns everything recorded, oldest first.
func (t *Trail) Entries() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Entry(nil), t.entries...)
}

// ForAgent returns the entries belonging to one agent.
//
// The question is almost always about one agent rather than all of them, because with eight running
// the interleaved trail is unreadable.
func (t *Trail) ForAgent(agentID string) []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []Entry
	for _, entry := range t.entries {
		if entry.AgentID == agentID {
			out = append(out, entry)
		}
	}
	return out
}

// Refused returns every call that did not run.
//
// Its own view because it answers a different question from the trail as a whole: not "what did
// this agent do" but "what did it try". An agent repeatedly attempting something it is not allowed
// to do is either misconfigured or being steered by something in its context, and both are worth
// noticing.
func (t *Trail) Refused() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []Entry
	for _, entry := range t.entries {
		if entry.Outcome == Deny || !entry.Ran {
			out = append(out, entry)
		}
	}
	return out
}

// Counts summarises the trail.
type Counts struct {
	Total    int
	Allowed  int
	Asked    int
	Refused  int
	Failed   int
	NotRun   int
	ByTool   map[string]int
	Duration time.Duration
}

// Count summarises what an agent has done.
func (t *Trail) Count(agentID string) Counts {
	t.mu.RLock()
	defer t.mu.RUnlock()

	counts := Counts{ByTool: map[string]int{}}
	for _, entry := range t.entries {
		if agentID != "" && entry.AgentID != agentID {
			continue
		}
		counts.Total++
		counts.ByTool[entry.Tool]++
		counts.Duration += entry.Duration

		switch entry.Outcome {
		case Allow:
			counts.Allowed++
		case Ask:
			counts.Asked++
		case Deny:
			counts.Refused++
		}
		if entry.Outcome != Deny && !entry.Ran {
			counts.NotRun++
		}
		if entry.Failed {
			counts.Failed++
		}
	}
	return counts
}
