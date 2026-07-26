// Package tui is the Bubble Tea interface.
//
// It reads through the core interfaces and nothing else, so it can run against the fake store
// with no real engine present. Models stay pure enough to test by feeding them messages and
// asserting on the resulting state.
//
// The visual language carries the product promise. Every state is distinguishable by word and
// glyph without color, stale reads as "ask me again" rather than as an alarm, and no single green
// indicator is ever allowed to hide which evidence is missing.
//
// Filled in by P1-07 and P2-09 onward.
package tui
