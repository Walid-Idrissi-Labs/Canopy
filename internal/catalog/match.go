package catalog

import "strings"

// Matching what somebody said against what a key can run.
//
// The rule, from D-46, is that spelling is forgiven and ambiguity is refused. Those pull in opposite
// directions and the order matters: every forgiving step is tried in turn, and the first one that
// finds anything is the answer, so "opus" cannot be dragged onto something else by a later, looser
// rule. When a step finds two things the caller is told there were two, because a guess that spawns
// the wrong model spends real money politely.

// normalise turns what somebody said into the shape a model id has.
//
// Case, spaces, underscores, dots and hyphens are all the same separator here. "Claude Sonnet 4.6",
// "claude sonnet 4 6" and "claude-sonnet-4-6" are one request typed three ways, and refusing two of
// them would be refusing over a distinction nobody intends. A slash survives, because it is part of
// the id on the gateways that namespace their models.
//
// The boundary between a letter and a digit is a separator too, so "sonnet5" and "gpt5.2" arrive at
// the same place "sonnet 5" and "gpt-5.2" do. Only in that direction: a digit followed by a letter
// is left alone, because "gpt-4o" is one word to the provider and splitting it would turn an id
// somebody typed correctly into one nothing answers to.
//
// Unexported, and used only by Match below. It was exported for a while with no caller outside this
// package, which is the shape D-44 asks to be justified or removed.
func normalise(spoken string) string {
	var b strings.Builder
	separated := false
	previousLetter := false

	for _, r := range strings.ToLower(strings.TrimSpace(spoken)) {
		letter := r >= 'a' && r <= 'z'
		digit := r >= '0' && r <= '9'

		switch {
		case letter || digit || r == '/':
			if digit && previousLetter {
				b.WriteRune('-')
			}
			b.WriteRune(r)
			separated = false
		case !separated && b.Len() > 0:
			b.WriteRune('-')
			separated = true
		}
		previousLetter = letter
	}
	return strings.TrimRight(b.String(), "-")
}

// familyPrefix is the one prefix people leave off, because it is on every model of one provider and
// nobody says it out loud. Nothing else is forgiven: dropping arbitrary leading words would make
// two different models answer to the same phrase.
const familyPrefix = "claude-"

// SameModel reports whether two ids are two spellings of one model.
//
// Exported because the keys store has to answer it at the moment somebody adds an id, and the answer
// has to be the one Match will give later: a store that accepted two spellings the matcher then
// treated as one would have collected a row nobody could ever select by name.
func SameModel(one, other string) bool { return normalise(one) == normalise(other) }

// Match returns the entries a spoken model name could mean.
//
// Zero means nobody here runs it and one means it is resolved. More than one is the answer that
// exists so a caller can refuse: it is what "opus" would return if two different opuses were the
// newest of their family, and picking between them is not something to do silently.
func Match(models []Model, spoken string) []Model {
	wanted := normalise(spoken)
	if wanted == "" {
		return nil
	}

	// Written the way the ids are written, first.
	if hits := matching(models, func(id, name string) bool {
		return id == wanted || (name != "" && name == wanted)
	}); len(hits) > 0 {
		return hits
	}

	// Then with the family prefix put back, which is how "sonnet 5" finds claude-sonnet-5.
	prefixed := familyPrefix + wanted
	if hits := matching(models, func(id, name string) bool {
		return id == prefixed || (name != "" && name == prefixed)
	}); len(hits) > 0 {
		return hits
	}

	// And last, a bare family word, which means the newest member of that family. The list's order
	// is what decides that, so this returns the first match rather than all of them: a family is not
	// an ambiguity, it is a question with a known answer.
	family := strings.TrimPrefix(wanted, familyPrefix)
	for _, model := range models {
		if familyOf(normalise(model.ID)) == family {
			return []Model{model}
		}
	}
	return nil
}

// matching collects the entries a predicate accepts, with ids compared normalised.
//
// Entries whose ids normalise to the same thing collapse to one, because the catalog and a key's own
// list can both name the same model and two of the same answer is not a conflict. Normalised rather
// than byte-exact, which is the comparison the matching itself uses: "claude-opus-5" and
// "CLAUDE-OPUS-5" are one model spelled twice, and reporting them as an ambiguity would refuse a
// request that has exactly one sensible reading.
//
// Only ids are collapsed on. Two models with different ids sharing a display name really are two
// answers, and that is the ambiguity this function exists to be able to report.
func matching(models []Model, accepts func(id, name string) bool) []Model {
	var hits []Model
	seen := make(map[string]bool)

	for _, model := range models {
		id := normalise(model.ID)
		if !accepts(id, normalise(model.Name)) {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		hits = append(hits, model)
	}
	return hits
}

// familyOf is the part of a model id that names its family rather than its version.
func familyOf(id string) string {
	rest := strings.TrimPrefix(id, familyPrefix)
	if cut := strings.IndexByte(rest, '-'); cut >= 0 {
		rest = rest[:cut]
	}
	return rest
}
