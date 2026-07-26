// Package tools is what agents can actually do.
//
// File, shell, git and web, each declaring its schema once so the provider call and the local
// argument validation cannot drift apart.
//
// Every tool is confined to its agent's worktree. Git is a structured tool rather than a shell
// string on purpose: a shell string hands the permission model an opaque blob that cannot be told
// apart from a force push, and confinement is enforceable per argument where in a string it is not
// enforceable at all.
//
// Filled in from A4-01 onward.
package tools
