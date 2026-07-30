// Package acp reaches Claude by driving the user's own Claude Code, not by authenticating to
// Anthropic.
//
// Everything about this package follows from one line on Anthropic's legal page: they do not permit
// third-party developers to offer Claude.ai login or to route requests through Free, Pro or Max plan
// credentials on behalf of their users, they enforce that on their servers, and they reserve the
// right to do so without prior notice. So there is no OAuth here, no claude.ai endpoint, no
// authorisation URL, no code exchange, and no token. There is a test that says so about the whole
// repository rather than only about this package, because the value of the promise is that a later
// contributor cannot quietly break it.
//
// What is permitted, and what Anthropic contemplates in writing, is the other thing: Claude Agent
// SDK, `claude -p` and third-party app usage still draw from the subscription's usage limits. That
// is a sentence about metering, and metering presupposes the usage. So Canopy delegates. It finds a
// Claude Code the user installed and signed in to themselves, starts the ACP bridge as a child
// process, and has a conversation with it over stdio. The subscription credential stays where the
// user put it. Canopy never reads it, never holds it, and has nowhere to put it: the credential
// record for this route is keys.KindDelegated, whose keychain half is empty and which refuses to be
// given a token at all.
//
// # What a delegated turn is not
//
// It is not a Canopy turn that happens to be answered elsewhere. Canopy's own tools, its per-agent
// trust levels, its permission prompts and its record of refused calls were all built on the
// assumption that Canopy runs the loop, and on this route somebody else does. That is Q-23, it is
// open, and this package answers the part of it that S-04 can answer by enforcing rather than by
// promising. See the comment on requestPermission and the "Providers and cost" section of
// LIMITATIONS.md. The short version, which belongs in front of anyone reading this code: the
// vendor's agent runs the turn and Canopy's gate is not in the path.
//
// # Protocol
//
// ACP, the Agent Client Protocol, is JSON-RPC 2.0 in newline-delimited JSON over the child's stdin
// and stdout. Canopy is the client and the bridge is the agent. Version 1 is what this speaks and
// what the specification says to ship; version 2 was published as a draft on 2026-07-20 with the
// explicit advice not to default to it in production. Every method name and every field name in
// wire.go was taken from the v1 schema and then confirmed against a live bridge rather than read
// off a blog post; see the notes on S-04 for how.
package acp
