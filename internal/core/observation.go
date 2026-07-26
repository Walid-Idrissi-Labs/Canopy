package core

// Observation is a three valued answer to a yes or no question about the world.
//
// It exists instead of a bool because the zero value of a bool is false, and false reads as "no".
// For a field like "is the process alive", a struct that was never filled in would then claim the
// process is dead, which is a statement Canopy did not observe. The zero value here is
// ObservationUnknown, so an unfilled field honestly says "we did not check".
type Observation uint8

const (
	// ObservationUnknown means the question was not answered. This is the zero value on purpose.
	ObservationUnknown Observation = iota
	// ObservationTrue means the answer was observed to be yes.
	ObservationTrue
	// ObservationFalse means the answer was observed to be no.
	ObservationFalse
)

// ObserveBool converts a definite answer into an Observation. Only call it where the answer
// really was observed.
func ObserveBool(v bool) Observation {
	if v {
		return ObservationTrue
	}
	return ObservationFalse
}

// IsTrue reports whether the answer was observed to be yes. Unknown is not yes.
func (o Observation) IsTrue() bool { return o == ObservationTrue }

// IsFalse reports whether the answer was observed to be no. Unknown is not no.
func (o Observation) IsFalse() bool { return o == ObservationFalse }

// IsKnown reports whether the question was answered at all.
func (o Observation) IsKnown() bool { return o != ObservationUnknown }

func (o Observation) String() string {
	switch o {
	case ObservationTrue:
		return "yes"
	case ObservationFalse:
		return "no"
	default:
		return "unknown"
	}
}
