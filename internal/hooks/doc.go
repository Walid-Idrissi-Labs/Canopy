// Package hooks runs a command when something actually happens.
//
// Tests go green, commit. Tests go red, tell me. An agent has been sitting there for ten minutes,
// nudge it. This is where verification and orchestration compound: the truth engine is what makes a
// trigger worth having, because a trigger firing on a state nobody established is worse than no
// trigger at all.
//
// Three rules hold the whole package up, and each of them is a way this could quietly go wrong.
//
// **A hook fires on an edge, not on a state.** The poller reports where every workspace stands every
// couple of seconds. Firing on the state would run an auto-commit hook every two seconds for as long
// as the tests stayed green. So a hook fires when a subject enters a state it was not in before, and
// the runner remembers where each subject was.
//
// **Nothing fires on stale or unknown evidence.** A green that no longer describes the code is the
// exact failure this project exists to refuse, and a hook that commits on the strength of one would
// take that failure and write it into history. Stale and unknown are recorded, so the edge is
// tracked, and they fire nothing. That also means passing, then stale, then passing fires twice,
// which is right: the second pass is new evidence about different code.
//
// **A hook fires at most once per revision.** Without this the obvious configuration is a loop: a
// commit on green moves HEAD, the revision changes, the results go stale, they re-run, they pass,
// and it commits again. Keying the memory on the revision as well as the state ends that after one
// pass, and it does so by construction rather than by a counter somebody has to tune.
//
// The v0.1 boundary:
//
//   - Hooks are per project and come from canopy.json. There is no user level hook file, because a
//     hook is a command that runs without being asked and the argument for the config file being
//     committed and reviewable applies to hooks more than to anything else in it.
//   - A hook is a shell command with no arguments passed to it. What happened is in the environment
//     rather than in the command line, so the command in the file is the command that runs and a
//     reviewer can read it without simulating a substitution.
//   - Nothing here is sandboxed, and hooks are not governed by the permission model. They are the
//     user's own commands from the user's own committed file, which is the repository trust
//     contract, not the agent one. A8-05 deliberately does not let an agent add a hook.
package hooks
