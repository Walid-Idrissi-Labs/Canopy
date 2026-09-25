package core

import "context"

type conversationKey struct{}

// WithConversation marks a context as belonging to one epoch of one conversation, so a tool can
// tell whether something it returned earlier is still in front of the model. The value is opaque:
// the engine derives it from the conversation and the number of compactions it has had, and a new
// epoch means earlier tool results may no longer be visible.
func WithConversation(ctx context.Context, epoch string) context.Context {
	return context.WithValue(ctx, conversationKey{}, epoch)
}

// ConversationFrom returns the conversation epoch a context was marked with, or "".
func ConversationFrom(ctx context.Context) string {
	epoch, _ := ctx.Value(conversationKey{}).(string)
	return epoch
}

type sessionKey struct{}

// WithSession marks a context with the conversation a tool call was made in, so a tool that acts
// on behalf of that conversation, dispatch above all, can say which one asked.
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// SessionFrom returns the conversation a context was marked with, or "".
func SessionFrom(ctx context.Context) string {
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}
