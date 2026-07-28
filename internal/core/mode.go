package core

// Modes are what an agent is doing, named.
//
// **A mode is a trust level, a prompt and a name, and it needs all three.** The level is what the
// permission layer enforces and is the only part a model cannot argue with. The prompt is what stops
// it wasting a turn discovering the level the hard way: an agent that has not been told it is
// planning will try to edit a file, be refused, try again, and burn a turn thrashing against a
// boundary nobody mentioned. Enforcement without a prompt is safe and expensive. A prompt without
// enforcement is theatre. Both, every time.
//
// The ladder is ordered by how much can go permanently wrong rather than by how much is allowed,
// which is why runway sits below cruise despite being the more capable of the two: it can run
// anything, and it cannot leave you with a workspace that does not build.

// Mode is a named posture.
type Mode struct {
	// Name is what the box shows and what the command takes.
	Name string

	// Description is the one line the menu and the help screen use.
	Description string

	// Trust is what the permission layer decides against. Two modes can share a level; the level is
	// what an agent may do and the mode is also how what it did is treated afterwards.
	Trust TrustLevel

	// Prompt is what the model is told it is doing, sent as the system prompt.
	Prompt string

	// KeepsGreen says a turn is only accepted if the workspace still verifies afterwards, and is
	// rolled back to the checkpoint if it does not.
	KeepsGreen bool

	// NeedsUndo says this mode is only offered where a turn can be taken back, which means a git
	// repository with checkpoints running. Refused rather than quietly downgraded where it cannot
	// be: a mode that silently became the more dangerous one below it would be the worst possible
	// failure of a safety setting.
	NeedsUndo bool
}

// The names, which are what a command takes and what the box shows.
const (
	ModePlan   = "plan"
	ModeBuild  = "build"
	ModeRunway = "runway"
	ModeCruise = "cruise"
)

// Modes returns the ladder, in the order the key cycles through it.
func Modes() []Mode {
	return []Mode{
		{
			Name:        ModePlan,
			Description: "read and think, change nothing",
			Trust:       TrustReadOnly,
			Prompt: `You are planning. Work out what should be done and say so, and do not do it.

You can read files and search the codebase. You cannot write, you cannot run commands, and you
cannot start other agents: all three are refused by the permission layer rather than by your own
restraint, so do not spend turns trying.

If you are asked to start agents, say plainly that this mode cannot and that shift+tab switches to
build, which can. The tool for it is not offered to you here, so saying you will do it and then not
doing it is the one answer that leaves somebody waiting for something that is never going to happen.

Say specifically which files you would change or create, which commands you would run written out as
you would run them, and anything you are unsure about along with what you would do if it turned out
differently. Then stop and wait to be told to go ahead.`,
		},
		{
			Name:        ModeBuild,
			Description: "edit freely, ask before running anything",
			Trust:       TrustStandard,
			// No prompt. This is the ordinary way to work and describing it to the model would spend
			// context saying that nothing unusual is going on.
		},
		{
			Name:        ModeRunway,
			Description: "edit and run freely, rolled back if it ends red",
			Trust:       TrustBroad,
			KeepsGreen:  true,
			NeedsUndo:   true,
			Prompt: `You can edit files and run commands without being asked each time.

The workspace is checked after every turn and put back as it was if the checks do not pass, so break
things while you are working if you need to, and do not stop with them broken. Run the project's own
tests before you finish rather than assuming.

If you cannot get back to a passing state, stop and say what is failing and why. Ending a turn red
costs the whole turn, so saying you are stuck is cheaper than hoping.`,
		},
		{
			Name:        ModeCruise,
			Description: "everything runs, nothing asks",
			Trust:       TrustBroad,
			NeedsUndo:   true,
			Prompt: `You can do anything without being asked, including commands that discard work.

Nobody is reviewing each step, so the judgement that would have been theirs is yours. Prefer the
reversible version of an action, say what you are about to do before doing the irreversible kind,
and stop and ask if you find yourself about to do something the request did not clearly cover.`,
		},
	}
}

// ModeByName returns a mode, and whether it exists.
func ModeByName(name string) (Mode, bool) {
	for _, mode := range Modes() {
		if mode.Name == name {
			return mode, true
		}
	}
	return Mode{}, false
}

// ModeNames is every mode name, for an error that tells somebody what they could have typed.
func ModeNames() []string {
	names := make([]string, 0, len(Modes()))
	for _, mode := range Modes() {
		names = append(names, mode.Name)
	}
	return names
}

// ModeForTrust is the mode a bare trust level corresponds to.
//
// Needed because trust is the older setting and agents are still configured with one directly. An
// agent set up read-only is planning whether or not anybody used the word, and the box should say
// so rather than claiming it is building.
//
// Broad maps to cruise rather than runway, which is the safe direction to be wrong in: cruise is
// what broad already meant before runway existed, and reading an existing configuration as the mode
// that silently reverts turns would be inventing a promise nobody made.
func ModeForTrust(level TrustLevel) Mode {
	switch {
	case level == TrustReadOnly:
		mode, _ := ModeByName(ModePlan)
		return mode
	case level.AtLeast(TrustBroad):
		mode, _ := ModeByName(ModeCruise)
		return mode
	default:
		mode, _ := ModeByName(ModeBuild)
		return mode
	}
}

// NextMode is the mode after this one in the cycle, wrapping at the end.
func NextMode(name string) Mode {
	modes := Modes()
	for i, mode := range modes {
		if mode.Name == name {
			return modes[(i+1)%len(modes)]
		}
	}
	return modes[0]
}
