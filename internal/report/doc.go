// Package report turns what an agent did into markdown somebody who was not watching can read.
//
// The artefact this produces is a pull request body. That is the whole design constraint, and it is
// a sharper one than it looks: a pull request body is read by somebody deciding whether to merge,
// usually quickly, usually trusting it. So the failure that matters here is not an ugly report, it
// is a confident one.
//
// Which is why the acceptance criterion for A8-08 is a single sentence about honesty rather than
// anything about formatting. **The report never claims a verification state the evidence does not
// support.** Everywhere else in Canopy an unverified state is a colour and a word on a screen that
// gets refreshed; here it is a paragraph that outlives the run, gets pasted into a pull request,
// and is read by somebody who cannot see the screen it came from.
//
// So the rules are stricter than the dashboard's, in one direction:
//
//   - Stale is never rendered as passing, and never quietly omitted either. A report that leaves out
//     the tests because they had gone stale reads as a change with no test story, which is a
//     different and more flattering lie than saying they went stale.
//   - An unranked agent says it is unranked and why. The ranking already refuses to place an agent
//     whose evidence it cannot stand behind, and that refusal is information a reviewer wants.
//   - A cost that could not be priced says so rather than reading as zero, and a total containing
//     one unpriced turn is reported as a floor rather than as a figure.
//   - Nothing in here computes a verdict of its own. Every state comes from the roll-up and the
//     ranking, which are the things that have tests proving they refuse to guess. A second opinion
//     assembled in a formatting package is how the two would come to disagree.
//
// The v0.1 boundary: this describes one agent's work at one moment. It does not diff two reports,
// does not talk to a forge, and does not open anything. Producing text and letting a person paste it
// is deliberate, because an unattended thing that opens pull requests is exactly what the scope
// reminder rules out.
package report
