package core

import "testing"

// The state vocabularies are a contract, not an implementation detail. The roll-up rules, the
// display wording in DECISIONS.md D-10 and the acceptance criteria are all written in terms of
// these exact strings, so these tests are deliberately written as literals rather than derived
// from the constants. A test that says AllTestStates() equals AllTestStates() would pass while
// somebody quietly renamed a state out from under the rest of the project.

func TestAllTestStatesMatchesTheContract(t *testing.T) {
	want := []TestState{
		"not-configured",
		"queued",
		"running",
		"passing",
		"failing",
		"stale",
		"cancelled",
		"error",
		"unknown",
	}

	got := AllTestStates()
	if len(got) != len(want) {
		t.Fatalf("test state vocabulary changed size: got %d states %v, want %d %v",
			len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("test state %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAllServiceStatesMatchesTheContract(t *testing.T) {
	want := []ServiceState{
		"not-configured",
		"stopped",
		"starting",
		"healthy",
		"unhealthy",
		"crashed",
		"stopping",
		"unknown",
	}

	got := AllServiceStates()
	if len(got) != len(want) {
		t.Fatalf("service state vocabulary changed size: got %d states %v, want %d %v",
			len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("service state %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAllWorkspaceOwnershipsMatchesTheContract(t *testing.T) {
	want := []WorkspaceOwnership{
		"primary",
		"managed",
		"adopted",
		"external-read-only",
	}

	got := AllWorkspaceOwnerships()
	if len(got) != len(want) {
		t.Fatalf("ownership vocabulary changed size: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ownership %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestVocabulariesHaveNoDuplicates(t *testing.T) {
	t.Run("test states", func(t *testing.T) {
		seen := map[TestState]bool{}
		for _, s := range AllTestStates() {
			if seen[s] {
				t.Errorf("duplicate test state %q", s)
			}
			seen[s] = true
		}
	})
	t.Run("service states", func(t *testing.T) {
		seen := map[ServiceState]bool{}
		for _, s := range AllServiceStates() {
			if seen[s] {
				t.Errorf("duplicate service state %q", s)
			}
			seen[s] = true
		}
	})
}

func TestEveryDeclaredStateIsValid(t *testing.T) {
	for _, s := range AllTestStates() {
		if !s.Valid() {
			t.Errorf("test state %q is in the vocabulary but Valid() says otherwise", s)
		}
	}
	for _, s := range AllServiceStates() {
		if !s.Valid() {
			t.Errorf("service state %q is in the vocabulary but Valid() says otherwise", s)
		}
	}
	for _, o := range AllWorkspaceOwnerships() {
		if !o.Valid() {
			t.Errorf("ownership %q is in the vocabulary but Valid() says otherwise", o)
		}
	}
	for _, k := range AllProbeKinds() {
		if !k.Valid() {
			t.Errorf("probe kind %q is in the vocabulary but Valid() says otherwise", k)
		}
	}
	for _, k := range AllEventKinds() {
		if !k.Valid() {
			t.Errorf("event kind %q is in the vocabulary but Valid() says otherwise", k)
		}
	}
	for _, s := range AllConfigStates() {
		if !s.Valid() {
			t.Errorf("config state %q is in the vocabulary but Valid() says otherwise", s)
		}
	}
	for _, s := range AllTrustStates() {
		if !s.Valid() {
			t.Errorf("trust state %q is in the vocabulary but Valid() says otherwise", s)
		}
	}
}

func TestUnknownStringsAreNotValid(t *testing.T) {
	if TestState("green").Valid() {
		t.Error("TestState(green) should not be valid, the vocabulary is closed")
	}
	if ServiceState("up").Valid() {
		t.Error("ServiceState(up) should not be valid, the vocabulary is closed")
	}
	if TestState("").Valid() {
		t.Error("the empty test state should not be valid")
	}
	if ServiceState("").Valid() {
		t.Error("the empty service state should not be valid")
	}
}

// Exactly one test state and one service state may contribute to a green roll-up. If a second one
// ever qualifies, the product has started lying somewhere.
func TestOnlyOneStateCountsAsGreen(t *testing.T) {
	var greenTests []TestState
	for _, s := range AllTestStates() {
		if s.IsGreen() {
			greenTests = append(greenTests, s)
		}
	}
	if len(greenTests) != 1 || greenTests[0] != TestPassing {
		t.Errorf("exactly one test state may be green, got %v", greenTests)
	}

	var greenServices []ServiceState
	for _, s := range AllServiceStates() {
		if s.IsGreen() {
			greenServices = append(greenServices, s)
		}
	}
	if len(greenServices) != 1 || greenServices[0] != ServiceHealthy {
		t.Errorf("exactly one service state may be green, got %v", greenServices)
	}
}

func TestStaleAndNotConfiguredAreNeverGreen(t *testing.T) {
	// Called out separately from the count above because these two are the states most likely to
	// be "helpfully" treated as green by a future change. Stale usually would have passed, and
	// nothing configured feels like nothing wrong.
	if TestStale.IsGreen() {
		t.Error("stale must never be green, it is the whole point of the freshness model")
	}
	if TestNotConfigured.IsGreen() {
		t.Error("no tests configured is not the same as tests passed")
	}
	if ServiceNotConfigured.IsGreen() {
		t.Error("no service configured is not the same as service healthy")
	}
}

func TestTerminalTestStates(t *testing.T) {
	terminal := map[TestState]bool{
		TestPassing:   true,
		TestFailing:   true,
		TestCancelled: true,
		TestError:     true,
	}
	for _, s := range AllTestStates() {
		if got, want := s.IsTerminal(), terminal[s]; got != want {
			t.Errorf("%q IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestDiscoveredOwnership(t *testing.T) {
	// Discovery finds the primary checkout and worktrees other tools made. It never produces
	// managed or adopted, because those mean Canopy created the worktree or was handed it.
	discovered := map[WorkspaceOwnership]bool{
		OwnershipPrimary:          true,
		OwnershipExternalReadOnly: true,
	}
	for _, o := range AllWorkspaceOwnerships() {
		if got, want := o.DiscoveredNotCreated(), discovered[o]; got != want {
			t.Errorf("%q DiscoveredNotCreated() = %v, want %v", o, got, want)
		}
	}
}

// The safety property that survives every scope change: Canopy may create and remove worktrees for
// its own agents, and may never remove one it merely found. The primary checkout is never
// removable under any feature.
func TestCanopyNeverRemovesWhatItDidNotCreate(t *testing.T) {
	for _, o := range AllWorkspaceOwnerships() {
		if o.DiscoveredNotCreated() && o.AllowsLifecycleOperations() {
			t.Errorf("%q was discovered rather than created, but allows lifecycle operations, "+
				"which would let Canopy destroy a worktree somebody else made", o)
		}
	}
	if OwnershipPrimary.AllowsLifecycleOperations() {
		t.Error("the primary checkout must never be removable")
	}
	if !OwnershipManaged.AllowsLifecycleOperations() {
		t.Error("Canopy creates managed worktrees for agents, so it has to be able to remove them")
	}
}

func TestOnlyGrantedTrustAllowsExecution(t *testing.T) {
	for _, s := range AllTrustStates() {
		want := s == TrustGranted
		if got := s.AllowsExecution(); got != want {
			t.Errorf("%q AllowsExecution() = %v, want %v", s, got, want)
		}
	}
}
