package core

import "testing"

// The reason Observation exists at all. A struct field nobody filled in must not claim a negative
// answer, because "the process is not alive" is a statement about the world that Canopy would not
// actually have observed.
func TestZeroObservationIsUnknown(t *testing.T) {
	var o Observation
	if o != ObservationUnknown {
		t.Fatalf("zero Observation = %v, want unknown", o)
	}
	if o.IsTrue() {
		t.Error("an unfilled observation must not read as yes")
	}
	if o.IsFalse() {
		t.Error("an unfilled observation must not read as no, that is a claim we never made")
	}
	if o.IsKnown() {
		t.Error("an unfilled observation is not known")
	}
}

func TestObserveBool(t *testing.T) {
	if got := ObserveBool(true); got != ObservationTrue {
		t.Errorf("ObserveBool(true) = %v, want true", got)
	}
	if got := ObserveBool(false); got != ObservationFalse {
		t.Errorf("ObserveBool(false) = %v, want false", got)
	}
	if !ObserveBool(false).IsKnown() {
		t.Error("an observed no is still an observation")
	}
}

func TestObservationString(t *testing.T) {
	tests := []struct {
		o    Observation
		want string
	}{
		{ObservationUnknown, "unknown"},
		{ObservationTrue, "yes"},
		{ObservationFalse, "no"},
		{Observation(99), "unknown"},
	}
	for _, tc := range tests {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("Observation(%d).String() = %q, want %q", tc.o, got, tc.want)
		}
	}
}

// A ServiceHealth that was never populated must not imply anything about the service.
func TestZeroServiceHealthClaimsNothing(t *testing.T) {
	var h ServiceHealth
	if h.ProcessAlive.IsKnown() {
		t.Error("zero ServiceHealth should not claim to know whether the process is alive")
	}
	if h.Ready.IsKnown() {
		t.Error("zero ServiceHealth should not claim to know whether the service is ready")
	}
	if h.State.IsGreen() {
		t.Error("zero ServiceHealth must never be green")
	}
}
