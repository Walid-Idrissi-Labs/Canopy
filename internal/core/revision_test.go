package core

import "testing"

func TestRevisionKeyKnown(t *testing.T) {
	if (RevisionKey{}).Known() {
		t.Error("the zero revision must be unknown, otherwise an uncomputed revision looks real")
	}
	if !(RevisionKey{HeadSHA: "abc"}).Known() {
		t.Error("a revision with a HeadSHA is known")
	}
	if (RevisionKey{DirtyDigest: "d1"}).Known() {
		t.Error("a dirty digest without a HeadSHA is not a usable revision")
	}
}

// This is the most important test in the package. Everything about freshness reduces to whether
// the revision a result was captured against still equals the current one, so each way Equal
// could be too permissive is a way to show a green result for code that has since changed.
func TestRevisionKeyEqual(t *testing.T) {
	clean := RevisionKey{HeadSHA: "abc123"}
	dirty := RevisionKey{HeadSHA: "abc123", DirtyDigest: "d1"}
	dirtier := RevisionKey{HeadSHA: "abc123", DirtyDigest: "d2"}
	moved := RevisionKey{HeadSHA: "def456"}
	unknown := RevisionKey{}

	tests := []struct {
		name string
		a, b RevisionKey
		want bool
	}{
		{"identical clean revisions", clean, clean, true},
		{"identical dirty revisions", dirty, dirty, true},
		{"different commit", clean, moved, false},
		{"same commit, one dirty", clean, dirty, false},
		{"same commit, different uncommitted work", dirty, dirtier, false},
		{"unknown against known", unknown, clean, false},
		{"known against unknown", clean, unknown, false},

		// If two unknowns compared equal, a result captured while the revision was uncomputable
		// would keep matching the current uncomputable revision, and would sit there green
		// forever. Unknown means "no evidence", and two absences of evidence do not make a match.
		{"unknown against unknown", unknown, unknown, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != tc.want {
				t.Errorf("%v.Equal(%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			// Equality has to be symmetric, or staleness would depend on argument order.
			if got := tc.b.Equal(tc.a); got != tc.want {
				t.Errorf("%v.Equal(%v) = %v, want %v (not symmetric)", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

func TestRevisionKeyClean(t *testing.T) {
	if !(RevisionKey{HeadSHA: "abc"}).Clean() {
		t.Error("a known revision with no dirty digest is clean")
	}
	if (RevisionKey{HeadSHA: "abc", DirtyDigest: "d"}).Clean() {
		t.Error("a revision with a dirty digest is not clean")
	}
	if (RevisionKey{}).Clean() {
		t.Error("an unknown revision is not clean, we did not observe that it was")
	}
}

func TestRevisionKeyShort(t *testing.T) {
	tests := []struct {
		name string
		key  RevisionKey
		want string
	}{
		{"unknown", RevisionKey{}, "unknown"},
		{"clean long sha", RevisionKey{HeadSHA: "abcdef1234567890"}, "abcdef1"},
		{"clean short sha", RevisionKey{HeadSHA: "abc"}, "abc"},
		{"dirty", RevisionKey{HeadSHA: "abcdef1234567890", DirtyDigest: "9f8e7d"}, "abcdef1+9f8e"},
		{"dirty short digest", RevisionKey{HeadSHA: "abcdef1234567890", DirtyDigest: "ab"}, "abcdef1+ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.key.Short(); got != tc.want {
				t.Errorf("Short() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A dirty revision must never display as the bare commit it sits on, or a user reading the
// dashboard would think a result covered committed code when it covered uncommitted work.
func TestRevisionShortDistinguishesDirtyFromClean(t *testing.T) {
	clean := RevisionKey{HeadSHA: "abcdef1234567890"}
	dirty := RevisionKey{HeadSHA: "abcdef1234567890", DirtyDigest: "9f8e7d"}
	if clean.Short() == dirty.Short() {
		t.Errorf("clean and dirty revisions on the same commit both display as %q", clean.Short())
	}
}
