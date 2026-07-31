// Package codex reaches ChatGPT by driving OpenAI's own Codex app server, not by authenticating to
// OpenAI.
//
// OpenAI is the one route of D-51's three with no third-party programme behind it, which is why the
// choice of surface carries most of the weight here. It does have a documented one. `codex
// app-server` is published under Apache-2.0 as part of the Codex CLI and is described by OpenAI as
// what to use for a deep integration inside your own product, covering authentication, conversation
// history, approvals and streamed agent events. So Canopy drives it, and lets it own both halves of
// the thing Canopy would otherwise have to hold: it hosts the ChatGPT sign-in itself, on its own
// loopback port, and it keeps the tokens afterwards in $CODEX_HOME. Canopy adds no listener, mints
// no authorisation URL, and never sees an access token. The credential this route stores is
// keys.KindDelegated for exactly that reason, and internal/keys refuses to put a token behind one.
//
// # Saying who is calling
//
// A client names itself at `initialize` through `clientInfo.name`, and that value becomes the
// originator the app server sends upstream. Canopy sends "canopy". It must never send
// `codex_cli_rs` or any other client's name, and that is not a style preference: impersonating
// another client is the single behaviour OpenAI's terms plausibly reach, none of the established
// projects on this path do it, and a route chosen because it is defensible stops being defensible
// the moment it lies about who is calling. The handshake is checkable rather than merely intended,
// because the app server echoes the composed user agent back in the `initialize` result, so Canopy
// reads its own name back and refuses to continue if what came back belongs to somebody else.
//
// # What a delegated turn is not
//
// It is not a Canopy turn that happens to be answered elsewhere. The app server runs its own tool
// loop, its own sandbox and its own approval model. Canopy's tools are not offered to it, because
// the protocol has no field for a client to supply any; the only tools in the room are the app
// server's own and whatever MCP servers the user configured in their own config.toml. Every
// approval request is declined. That is Q-23, it is open, and this package answers the part of it
// that can be enforced rather than promised. The sentence that belongs in front of anyone reading
// this code, and which is in LIMITATIONS.md in the same words: the vendor's agent runs the turn and
// Canopy's gate is not in the path.
//
// # Protocol
//
// JSON-RPC 2.0 in newline-delimited JSON over the child's stdin and stdout, one document per line.
// Canopy is the client and `codex app-server` is the server, and traffic goes both ways: the server
// sends notifications for everything that happens during a turn, and requests for the approvals
// Canopy declines. Every method name, field name and enum value in wire.go was taken from the
// schema the binary generates for itself with `codex app-server generate-json-schema` and then
// confirmed by driving a real app server against a real ChatGPT account. See the notes on S-05 for
// what that turned up that a schema alone would not have.
package codex
