package tui

// The terminal bell, for the moment an agent starts needing a person.
//
// The point of the program is that several agents run while you are looking at one of them, so the
// interesting event is one you are by definition not watching. The header says who needs you from
// every screen, which answers it for somebody at the keyboard; this answers it for somebody who has
// walked away from the desk, and it is the only thing in Canopy that makes a noise.
//
// Off unless it is asked for, and asked for through the environment rather than a file, for the
// reason theme.go gives about the palette: the terminal is where this kind of decision is already
// made for every other program on the machine, and how loud a program is belongs to the person
// sitting in front of it rather than to the repository they happen to have open.

import (
	"io"
	"os"
)

// BellEnv is the variable that turns the bell on. Present and not "0" is the convention NO_COLOR
// set and the rest of the ecosystem follows, so CANOPY_BELL= empty is off and CANOPY_BELL=1 is on.
const BellEnv = "CANOPY_BELL"

// bellOut is where the bell is written.
//
// Standard error rather than standard output, which the renderer owns: a byte written into the
// frame stream would be part of a frame, diffed against the last one and redrawn or dropped
// depending on what else changed. The bell is not a picture and has no business going through the
// thing that draws pictures.
//
// A variable only so a test can hear it, which is the same reason modeSettleDelay is one. Nothing
// outside a test changes it.
var bellOut io.Writer = os.Stderr

// bellWanted reports whether the person running this asked to be told out loud.
func bellWanted() bool {
	value, present := os.LookupEnv(BellEnv)
	return present && value != "" && value != "0"
}

// ring sounds the bell once, or does nothing when it was never asked for.
//
// Written where it is decided rather than handed back as a command to be run later. One byte to a
// stream nothing else is writing to is cheaper than the clipboard write the conversation already
// does from inside its own update, and a bell that arrives a frame after the thing it is announcing
// would be the one piece of this the timing actually matters for.
//
// A failure is ignored on purpose. Nobody is helped by an error message saying the terminal could
// not be beeped at.
func ring() {
	if !bellWanted() {
		return
	}
	// BEL, which every terminal since the teletype answers with a sound or a visual flash,
	// according to what its user has configured. Which of the two it is is deliberately not this
	// program's decision to make.
	_, _ = io.WriteString(bellOut, "\a")
}
