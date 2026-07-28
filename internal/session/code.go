package session

import "strings"

// What a conversation is called when a person has to type it.
//
// Printed when Canopy exits and taken by `canopy pickup`, which is the resume path for 0.1. A chat
// picker comes later; until it does, this is the whole of how somebody gets back to what they were
// doing, so it has to be short enough to read off a terminal and retype without checking twice.

// Code is the conversation's number, out of its generated ID.
//
// It is tempting to mint something that looks like a token, four random characters that feel like a
// handle you could send to somebody, and that would be a lie about what this is. A token needs a
// table mapping it back, the table is one more thing that can disagree with the conversation it
// names, and it would still only work on the machine that generated it, because the history database
// is local. This is honest about being a local counter and is shorter to type than the alternative.
func Code(sessionID string) string {
	if rest, ok := strings.CutPrefix(sessionID, idPrefix); ok {
		return rest
	}
	return sessionID
}

// SessionID turns a printed code back into the conversation it names.
//
// Both forms are accepted. The code is printed on its own, and the full ID turns up in `canopy
// search` output and in anything anybody pastes from a log, so somebody who types the longer one has
// not made a mistake and should not be told they have.
//
// Nothing is validated here beyond the shape. Whether the conversation exists is a question for
// storage, and answering it here would mean this function needed a database.
func SessionID(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if strings.HasPrefix(code, idPrefix) {
		return code
	}
	return idPrefix + code
}

// idPrefix is what generated session IDs begin with, in one place so Code and SessionID cannot come
// to disagree with each other or with the engine that mints them.
const idPrefix = "session-"
