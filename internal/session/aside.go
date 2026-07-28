package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Asking something without joining the conversation.
//
// The other half of steering. Steering changes what the agent does; this is for the times you want
// to know something and deliberately want to change nothing: what does this function do, why did it
// pick that library, is this the file I think it is. Asked as an ordinary message those questions
// cost a turn, land in the history, and become context every later turn carries.
//
// So this runs its own request against the conversation's own history and records none of it. The
// history is copied and read; nothing is written back, no turn is created, no event is published,
// and an agent working at the time goes on working. What comes back is text for the person who
// asked, and the model in the next real turn has no idea it was asked.
//
// No tools, and that is what makes it safe to run beside a turn in flight rather than merely polite.
// A side question that could call a tool would be a second agent operating on the same worktree with
// no checkpoint of its own, which is exactly the situation Canopy exists to stop people getting into
// by accident.

// AsideLimit bounds the reply.
//
// Short on purpose. This is a question asked in passing and the answer is read in the message area
// of a chat screen, so an essay would be both unwelcome and unreadable there. Anything that deserves
// a longer answer deserves a turn.
const AsideLimit = 600

// Aside answers a question from a conversation's context without joining it.
func (e *Engine) Aside(ctx context.Context, sessionID, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("there is no question here")
	}

	e.mu.Lock()
	stored, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return "", fmt.Errorf("there is no conversation %s", sessionID)
	}
	// Copied under the lock, because the turn in flight is appending to this very session while the
	// question is being asked, which is the whole point of the feature and would otherwise be a race.
	snapshot := copySession(*stored)
	resolver := e.resolver
	e.mu.Unlock()

	client, _, err := resolver.Resolve(snapshot.KeyName, snapshot.Model)
	if err != nil {
		return "", err
	}

	messages := append(snapshot.History(), core.Message{Role: core.RoleUser, Text: question})

	stream, err := client.Stream(ctx, core.Request{
		Model:    snapshot.Model,
		System:   asidePrompt,
		Messages: messages,
		// No tools. Not an omission: see the note at the top of this file.
		MaxTokens: AsideLimit,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()

	var answer strings.Builder
	for stream.Next() {
		if event := stream.Event(); event.Kind == core.EventText {
			answer.WriteString(event.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return "", err
	}

	text := strings.TrimSpace(answer.String())
	if text == "" {
		return "", errors.New("the model answered with nothing")
	}
	return text, nil
}

// asidePrompt tells the model what kind of answer this is.
//
// The instruction not to act matters more than the instruction to be brief. Given a conversation
// full of work in progress and a question about it, a coding model's reflex is to start fixing
// whatever the question was about, and here it has no tools to fix anything with, so the result
// would be a page describing edits nobody asked for and nobody is going to apply.
const asidePrompt = `You are being asked a question on the side, about the conversation above.

Answer the question and nothing else. Do not propose changes, do not write code unless the question
is asking to see some, and do not carry on with whatever the conversation was doing. Nothing you say
here is kept: the person asking will read it and the conversation continues without it.

Be brief. This is being read in the margin of a chat screen, not as a reply.`
