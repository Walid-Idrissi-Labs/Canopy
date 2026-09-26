package chat

// A failed turn says what to do next under it: enter to try again when trying again can work,
// counting down when the provider asked for a wait, and what to fix instead when it cannot.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// retryTickMsg redraws a countdown once a second while it runs.
type retryTickMsg struct{ generation int }

// failedTurn is the last turn when it failed and nothing is typed or running, and no other
// agent's question is waiting on the same enter.
func (m Model) failedTurn() (core.Turn, bool) {
	if !m.input.Empty() || m.working || m.awaiting || len(m.visitors) > 0 {
		return core.Turn{}, false
	}
	session, ok := m.engine.Session(m.sessionID)
	if !ok || len(session.Turns) == 0 {
		return core.Turn{}, false
	}
	last := session.Turns[len(session.Turns)-1]
	return last, last.State == core.TurnFailed && !last.Retried
}

// retryWait is how much longer the provider asked to be left alone, or zero.
func retryWait(turn core.Turn, now time.Time) time.Duration {
	if turn.RetryAfter <= 0 || turn.EndedAt.IsZero() {
		return 0
	}
	if left := turn.RetryAfter - now.Sub(turn.EndedAt); left > 0 {
		return left
	}
	return 0
}

// retryCard is the line under a failed turn.
func (m Model) retryCard() []string {
	turn, ok := m.failedTurn()
	if !ok {
		return nil
	}
	t := theme.Current()
	if turn.ErrorKind != "" && !turn.ErrorKind.Retryable() {
		return []string{t.Warning.Render("  ") + t.Body.Render(retryAdviceFor(turn, m.keyName))}
	}
	if wait := retryWait(turn, time.Now()); wait > 0 {
		return []string{t.Muted.Render("  the provider asked to wait " + wait.Round(time.Second).String() +
			" before trying again")}
	}
	why := ", or type something else to send instead"
	if turn.ErrorKind == core.ErrNetwork {
		why = "; the network failed, so the request got no answer"
	}
	return []string{t.Key.Render("  enter") + t.Body.Render(" tries it again") + t.Muted.Render(why)}
}

// retryAdviceFor says what to change for a failure retrying cannot fix, naming the credential.
func retryAdviceFor(turn core.Turn, keyName string) string {
	switch turn.ErrorKind {
	case core.ErrAuthentication:
		return "the credential " + keyName + " was refused; ctrl+k to check or replace it, then send again"
	case core.ErrContextLength:
		return "the conversation no longer fits the model; /compact summarises it, then send again"
	case core.ErrInvalidRequest:
		return "the provider refused the request as malformed, so trying it again would fail the same way"
	}
	return "trying this again would fail the same way"
}

// retry tries the failed turn again.
func (m Model) retry() (Model, tea.Cmd) {
	if _, err := m.engine.Retry(m.sessionID); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.err, m.notice = "", "trying again"
	m.scroll = 0
	m.refresh()
	return m, nil
}

// retryCountdown starts the redraw that counts a provider's wait down, when there is one.
func (m *Model) retryCountdown() tea.Cmd {
	turn, ok := m.failedTurn()
	if !ok || retryWait(turn, time.Now()) == 0 || m.retryTicking {
		return nil
	}
	m.retryTicking = true
	m.retryGeneration++
	generation := m.retryGeneration
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return retryTickMsg{generation: generation} })
}
