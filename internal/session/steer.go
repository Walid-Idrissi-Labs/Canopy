package session

// Steering and interrupting, which are two things and must stay two things.
//
// **Steer** queues guidance and lets the turn in flight finish normally. The guidance arrives at the
// next turn boundary, as an ordinary message, and the agent carries on from where it got to.
// **Interrupt** stops the turn now, keeps whatever text arrived and marks it interrupted.
//
// The distinction is the whole feature. Building only interrupt and calling it steering demos
// perfectly well and is useless in practice, because the way you correct an agent then is to throw
// away the work in progress, which usually means throwing away the reasoning that led to it. An
// agent that is three tool calls into something and has just read the wrong file does not need to
// start again; it needs to be told, at the next place where being told is possible.
//
// A message and not a system note, deliberately. The guidance is part of the conversation, so it is
// visible in the transcript, it is what the next turn's context is built from, and there is no
// second channel for a user to reach the model through that behaves differently from the first.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Steer queues guidance to be delivered when the current turn ends.
//
// Never cancels anything. If nothing is running the guidance is sent immediately, because a person
// who typed a correction into an idle session meant to send a message, and holding it back waiting
// for a turn that is not coming would look like the input being swallowed.
func (e *Engine) Steer(sessionID, guidance string) error {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return errors.New("there is nothing to steer with")
	}

	e.mu.Lock()
	session, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("no session %q", sessionID)
	}
	_, running := session.Active()
	if !running {
		e.mu.Unlock()
		_, err := e.Send(sessionID, guidance)
		return err
	}

	if e.steering == nil {
		e.steering = make(map[string][]string)
	}
	e.steering[sessionID] = append(e.steering[sessionID], guidance)
	e.mu.Unlock()

	// Published so the interface can show the guidance as queued the moment it is typed. Somebody who
	// steers and sees nothing happen assumes it did not register and says it again.
	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	return nil
}

// Steering returns the guidance waiting for a session, oldest first.
func (e *Engine) Steering(sessionID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.steering[sessionID]...)
}

// ClearSteering drops queued guidance and returns what was dropped.
//
// Returned rather than discarded, so the interface can put the text back in the message box. Guidance
// somebody typed and then cancelled is still something they wrote, and losing it to a keystroke is
// the kind of small theft that makes a tool feel unsafe.
func (e *Engine) ClearSteering(sessionID string) []string {
	e.mu.Lock()
	queued := e.steering[sessionID]
	delete(e.steering, sessionID)
	e.mu.Unlock()

	if len(queued) > 0 {
		e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	}
	return queued
}

// deliverSteering sends any queued guidance for a session, and reports whether it sent anything.
//
// Called after a turn closes out, from the turn's own goroutine. The queue is drained under the lock
// before anything is sent, so two turns finishing close together cannot both deliver the same
// guidance.
func (e *Engine) deliverSteering(sessionID string) bool {
	e.mu.Lock()
	queued := e.steering[sessionID]
	delete(e.steering, sessionID)
	e.mu.Unlock()

	if len(queued) == 0 {
		return false
	}

	// Joined into one message rather than sent as several turns. Three corrections typed while one
	// answer streamed are three parts of one thought, and sending them as three turns would have the
	// agent answer the first before it has read the third.
	if _, err := e.Send(sessionID, strings.Join(queued, "\n\n")); err != nil {
		// The send failed, so the guidance has not been delivered and must not be silently dropped.
		// Put back at the front, because it was written before anything that arrived while this ran.
		e.mu.Lock()
		if e.steering == nil {
			e.steering = make(map[string][]string)
		}
		e.steering[sessionID] = append(queued, e.steering[sessionID]...)
		e.mu.Unlock()
		return false
	}
	return true
}
