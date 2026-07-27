package core

// Ranking several agents on the same task, and the queue of work that is ready to read.
//
// The shapes live in the contract rather than in the engine that produces them, because the whole
// point of a ranking is that somebody sees it. A placement that could not be rendered without the
// screen knowing how the ranking was computed would be a leaderboard with a private scoring
// function, which is the thing this refuses to be.

// Placement is where one agent came, or why it could not be placed.
type Placement struct {
	Agent string

	// Rank is one based, and zero for an agent that could not be ranked at all.
	//
	// Zero rather than a large number, so that "no placement" can never sort as "came last". The two
	// mean different things: last is a result, and no placement is the absence of one.
	Rank int

	// Tests is the aggregate visible state of this agent's evidence.
	Tests TestState

	// Passing and Required count the required tests, which are the only ones that can block a green.
	Passing  int
	Required int

	Diff     DiffStat
	Revision RevisionKey

	// Reason explains the placement, or the refusal to make one. Never empty. A ranking whose order
	// cannot be accounted for is one nobody should act on.
	Reason string
}

// Ranked reports whether this agent was given a position.
func (p Placement) Ranked() bool { return p.Rank > 0 }

// Ranking is the result of comparing several agents on the same task.
type Ranking struct {
	// Ranked is best first. Empty is a legitimate answer, and means no agent currently has evidence
	// that describes its own code.
	Ranked []Placement

	// Unranked are the agents whose evidence cannot support a placement, each with the reason.
	// Listed rather than dropped, because an agent that has vanished from the screen is one somebody
	// will assume failed.
	Unranked []Placement
}

// Best returns the winning agent, if there is one.
func (r Ranking) Best() (Placement, bool) {
	if len(r.Ranked) == 0 {
		return Placement{}, false
	}
	return r.Ranked[0], true
}

// All returns every agent, ranked first and then unranked, which is the order they are displayed in.
func (r Ranking) All() []Placement {
	out := make([]Placement, 0, len(r.Ranked)+len(r.Unranked))
	out = append(out, r.Ranked...)
	return append(out, r.Unranked...)
}

// ReadyForReview is one agent waiting to be read.
type ReadyForReview struct {
	Agent  string
	Branch string
	Diff   DiffStat

	// Why says what makes this worth looking at, in the same terms the ranking uses.
	Why string
}
