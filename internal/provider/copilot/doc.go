// Package copilot runs a turn on the user's own GitHub Copilot subscription.
//
// This is the first of D-51's three sanctioned routes and the only one the vendor documents for
// exactly this case: register an app, have the user authorise it, hand the SDK their token, and the
// requests are made on their behalf against their own seat. Canopy speaks to the official Go SDK
// rather than to the endpoints underneath it, because being sanctioned is the whole value of the
// route and the sanctioned surface is the SDK.
//
// Three things about the shape of that SDK decide most of what is in here.
//
// It is session-shaped and core.ProviderClient is request-shaped. The SDK owns a conversation:
// create a session, send a prompt, listen. Canopy's contract hands over the whole message list every
// call and expects a stream back. There is no API for seeding history, so a client that made a
// session per turn would forget everything between turns. What this package does instead is hold one
// session per conversation and send only what the session has not already heard. The consequence is
// real and is written down in LIMITATIONS.md: on this route the conversation lives in GitHub's
// runtime, so Canopy's history editing, re-rolls and compaction do not reach it.
//
// It streams by callback and core.Stream is a pull iterator. A goroutine per session drains the
// SDK's handler into a channel, and each turn's stream reads from that channel until the session
// goes idle or asks for a tool. Nothing is buffered per turn, so a reply that arrives faster than it
// is read applies backpressure to the reader rather than growing without bound.
//
// It will happily give the model a shell. ModeEmpty plus an explicit allowlist is what stops it:
// no built-in tools, no MCP, no environment context, no skills, no file hooks, no host git. The only
// tools in the session are Canopy's own, declared with no handler so that every call comes back out
// to Canopy, through A4's permission gate, into Canopy's own implementation, and only then back down
// as a result. That is what keeps a delegated turn honest about what it is: GitHub's agent decides
// what to do, and Canopy's rules decide what may actually happen to somebody's files. Q-23 asks what
// Canopy's tools and permissions mean in a delegated turn, and for this route the answer is that
// they mean what they always meant.
//
// What this package never does: it does not open a browser, it does not listen on a port, it does
// not reuse another program's GitHub token, and it does not pretend to be another editor. The device
// flow is Canopy's own app or it is nothing.
package copilot
