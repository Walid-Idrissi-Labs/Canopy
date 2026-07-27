// Package mcp connects to Model Context Protocol servers and presents their tools as ordinary
// Canopy tools.
//
// One protocol buys an entire ecosystem of tools other people maintain, which is the whole reason
// this exists. It is also the reason it is the most dangerous package in the tree: everything here
// runs code somebody else wrote, described by metadata somebody else wrote, on the user's machine
// and under the user's account.
//
// So the governing rule is that a third party tool gets **more** scrutiny than a built in one and
// never less. Nothing in this package can lower a permission requirement. A server describes what
// its tools do; it does not get a say in what they are allowed to do. See kindFor in tool.go, which
// is where that is enforced and where the argument for it is written down.
//
// A failing server degrades that server only. Servers are dialled independently, a server that will
// not start or will not answer contributes no tools and one recorded failure, and a server that
// dies mid session turns its own calls into results the model can act on rather than into an error
// that ends the turn.
//
// The v0.1 boundary:
//
//   - Stdio transport only. HTTP and SSE transports are not implemented. Stdio is what local servers
//     use and it is the one that does not need a decision about credentials in a committed file.
//   - Tools only. Prompts, resources, sampling and roots are part of MCP and are not exposed.
//     Sampling in particular lets a server ask the model for a completion, which is a spend and a
//     context path we are not opening without a decision.
//   - No server side notifications are acted on, including tools/list_changed. The tool set is
//     whatever the server reported when Canopy connected. A server that changes its tools mid
//     session is not followed, because a tool appearing in an agent's registry after the user
//     approved the set is not something to do quietly.
//   - Process group teardown is A9-01's, not this package's. Shutdown here closes stdin, waits, then
//     cancels, which handles a well behaved server and a hung one. It does not chase grandchildren:
//     a server started through a launcher that forks can still leave one behind. This should move to
//     the shared helper when A9-01 lands rather than growing a second copy of that logic here.
package mcp
