package catalog

import "strings"

// Matching what somebody said against what a key can run.
//
// The rule, from D-46, is that spelling is forgiven and ambiguity is refused. Those pull in opposite
// directions and the order matters: every forgiving step is tried in turn, and the first one that
// finds anything is the answer, so "opus" cannot be dragged onto something else by a later, looser
// rule. When a step finds two things the caller is told there were two, because a guess that spawns
// the wrong model spends real money politely.

// Normalise turns what somebody said into the shape a model id has.
//
// Case, spaces, underscores, dots and hyphens are all the same separator here. "Claude Sonnet 4.6",
// "claude sonnet 4 6" and "claude-sonnet-4-6" are one request typed three ways, and refusing two of
// them would be refusing over a distinction nobody intends. A slash survives, because it is part of
// the id on the gateways that namespace their models.
func Normalise(spoken string) string {
	var b strings.Builder
	separated := false

	for _, r := range strings.ToLower(strings.TrimSpace(spoken)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '/':
			b.WriteRune(r)
			separated = false
		case !separated && b.Len() > 0:
			b.WriteRune('-')
			separated = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// familyPrefix is the one prefix people leave off, because it is on every model of one provider and
// nobody says it out loud. Nothing else is forgiven: dropping arbitrary leading words would make
// two different models answer to the same phrase.
const familyPrefix = "claude-"

// Match returns the entries a spoken model name could mean.
//
// Zero means nobody here runs it and one means it is resolved. More than one is the answer that
// exists so a caller can refuse: it is what "opus" would return if two different opuses were the
// newest of their family, and picking between them is not something to do silently.
func Match(models []Model, spoken string) []Model {
	wanted := Normalise(spoken)
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
		if familyOf(Normalise(model.ID)) == family {
			return []Model{model}
		}
	}
	return nil
}

// matching collects the entries a predicate accepts, with ids compared normalised.
//
// Entries sharing an id collapse to one, because the catalog and a key's own list can both name the
// same model and two of the same answer is not a conflict.
func matching(models []Model, accepts func(id, name string) bool) []Model {
	var hits []Model
	seen := make(map[string]bool)

	for _, model := range models {
		if !accepts(Normalise(model.ID), Normalise(model.Name)) {
			continue
		}
		if seen[model.ID] {
			continue
		}
		seen[model.ID] = true
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
