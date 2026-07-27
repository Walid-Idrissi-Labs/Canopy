package session

// Budgets, and the one thing that makes them a guardrail rather than a receipt.
//
// The cap is checked before the request goes out, never after it comes back. Checked afterwards it
// is a report of money already spent, which is a different product: it tells you what happened and
// it did not stop it happening. An agent in a tool loop can spend a lot between two glances at the
// screen, and the whole reason to have a number is to be stopped at it.
//
// A paused agent is paused, not cancelled. Whatever it had done is still there, its transcript is
// intact, and raising the cap continues from where it stopped. Cancelling instead would make the
// budget destroy the work it was protecting the value of.

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Budget is a spending cap.
//
// A zero Limit means no cap, which is the default. Refusing to run without a budget would be the
// rare case taxing the common one, and a number somebody has not chosen is not a limit, it is a
// surprise waiting to happen.
type Budget struct {
	// Limit is the ceiling in dollars. Zero means no limit.
	Limit float64

	// Spent is what has been billed against it so far.
	Spent float64

	// Paused is set once the limit is reached, and cleared by raising it.
	Paused bool

	// Unpriced counts requests that could not be costed, on a profile whose rate Canopy does not
	// know. They are counted rather than ignored, so a budget that is quietly meaningless says so
	// instead of reading as one that has not been touched.
	Unpriced int
}

// Remaining is what is left, and is zero rather than negative once the limit is passed.
func (b Budget) Remaining() float64 {
	if b.Limit <= 0 {
		return 0
	}
	if left := b.Limit - b.Spent; left > 0 {
		return left
	}
	return 0
}

// Capped reports whether a limit was set at all.
func (b Budget) Capped() bool { return b.Limit > 0 }

// Reliable reports whether the number can be trusted as a picture of what was spent.
//
// False as soon as anything ran on a profile with no known rate. A budget that has silently not
// been counting half the requests is worse than no budget, because it reads as reassurance.
func (b Budget) Reliable() bool { return b.Unpriced == 0 }

// Status is the line shown to a user.
func (b Budget) Status() string {
	switch {
	case !b.Capped() && b.Unpriced > 0:
		return fmt.Sprintf("$%.2f spent, %d requests on a profile with no known rate", b.Spent, b.Unpriced)
	case !b.Capped():
		return fmt.Sprintf("$%.2f spent, no cap set", b.Spent)
	case b.Paused:
		return fmt.Sprintf("paused at the $%.2f cap, $%.2f spent", b.Limit, b.Spent)
	case !b.Reliable():
		return fmt.Sprintf("$%.2f of $%.2f, but %d requests could not be costed so this is a floor",
			b.Spent, b.Limit, b.Unpriced)
	default:
		return fmt.Sprintf("$%.2f of $%.2f", b.Spent, b.Limit)
	}
}

// ErrPaused is returned when a session has reached its cap.
var ErrPaused = errors.New("this agent has reached its spending cap")

// budgets holds the caps, keyed by session, plus one across the whole run.
type budgets struct {
	mu      sync.Mutex
	session map[string]*Budget
	overall Budget
}

func newBudgets() *budgets {
	return &budgets{session: make(map[string]*Budget)}
}

// SetBudget puts a cap on one session. A limit of zero removes it.
//
// Raising the cap on a paused session resumes it, which is the whole point of pausing rather than
// cancelling: the answer to "it stopped" is one number away.
func (e *Engine) SetBudget(sessionID string, limit float64) error {
	if limit < 0 {
		return errors.New("a spending cap cannot be negative")
	}

	e.budgets.mu.Lock()
	budget, ok := e.budgets.session[sessionID]
	if !ok {
		budget = &Budget{}
		e.budgets.session[sessionID] = budget
	}
	budget.Limit = limit
	if limit == 0 || budget.Spent < limit {
		budget.Paused = false
	}
	e.budgets.mu.Unlock()

	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	return nil
}

// SetOverallBudget caps the whole run across every agent.
//
// Separate from the per session cap because they answer different questions. A per agent cap stops
// one agent running away; an overall cap is what somebody sets when they start six of them and care
// about the total rather than about which one spent it.
func (e *Engine) SetOverallBudget(limit float64) error {
	if limit < 0 {
		return errors.New("a spending cap cannot be negative")
	}

	e.budgets.mu.Lock()
	e.budgets.overall.Limit = limit
	if limit == 0 || e.budgets.overall.Spent < limit {
		e.budgets.overall.Paused = false
	}
	e.budgets.mu.Unlock()
	return nil
}

// Budget returns the cap and spend for a session.
func (e *Engine) Budget(sessionID string) Budget {
	e.budgets.mu.Lock()
	defer e.budgets.mu.Unlock()

	if budget, ok := e.budgets.session[sessionID]; ok {
		return *budget
	}
	return Budget{}
}

// OverallBudget returns the cap and spend across every agent.
func (e *Engine) OverallBudget() Budget {
	e.budgets.mu.Lock()
	defer e.budgets.mu.Unlock()
	return e.budgets.overall
}

// checkBudget reports why a session may not send, or nil.
//
// Called immediately before a request goes out. The margin question, whether the next request would
// take it over, is deliberately not asked: the cost of a request is not known until it comes back,
// and refusing on a guess would stop an agent that had budget left. What is refused is a session
// that is already at or past its cap, which is a fact rather than a prediction.
func (e *Engine) checkBudget(sessionID string) error {
	e.budgets.mu.Lock()
	defer e.budgets.mu.Unlock()

	if budget, ok := e.budgets.session[sessionID]; ok && budget.Capped() && budget.Spent >= budget.Limit {
		budget.Paused = true
		return fmt.Errorf("%w: $%.2f of a $%.2f cap. Raise the cap to carry on",
			ErrPaused, budget.Spent, budget.Limit)
	}
	if e.budgets.overall.Capped() && e.budgets.overall.Spent >= e.budgets.overall.Limit {
		e.budgets.overall.Paused = true
		return fmt.Errorf("%w: $%.2f of a $%.2f cap across every agent. Raise the cap to carry on",
			ErrPaused, e.budgets.overall.Spent, e.budgets.overall.Limit)
	}
	return nil
}

// recordSpend adds a turn's cost to the budgets it counts against.
func (e *Engine) recordSpend(sessionID string, usage core.Usage) {
	e.budgets.mu.Lock()
	defer e.budgets.mu.Unlock()

	budget, ok := e.budgets.session[sessionID]
	if !ok {
		budget = &Budget{}
		e.budgets.session[sessionID] = budget
	}

	if !usage.CostKnown {
		// Counted, not ignored. A cap that has silently not been counting half the requests reads
		// as reassurance, which is the worst thing a number can do.
		budget.Unpriced++
		e.budgets.overall.Unpriced++
		return
	}
	budget.Spent += usage.CostUSD
	e.budgets.overall.Spent += usage.CostUSD
}
