package core

import (
	"reflect"
	"testing"
)

// What an event is allowed to carry, asserted rather than described.
//
// Events are notifications and the snapshot is the truth, which is written down in three places and
// enforced in none. Two things depend on it and both fail quietly. Coalescing is only safe because
// replacing one notification with a later one loses nothing, which stops being true the moment an
// event carries something the snapshot does not. And a queue of events is only cheap because each
// one is a handful of identifiers, which stops being true the moment one carries a line of output, a
// diff or a model's reply: a burst of a thousand then costs a thousand copies of it, and the
// interface stops redrawing while it works through them.
//
// So this is a list, and a new field has to be added to it deliberately. That is the point. Somebody
// adding an Output or a Text field here would be making a decision about memory under load, and this
// test is what turns that into a conversation rather than a commit.
func TestEventsCarryIdentifiersRatherThanContent(t *testing.T) {
	allowed := map[string]string{
		"Sequence":    "uint64",
		"At":          "time.Time",
		"Kind":        "core.EventKind",
		"WorkspaceID": "string",
		"TestName":    "string",
		"ServiceName": "string",
		"RunID":       "string",
		"BufferID":    "string",
		"SessionID":   "string",
		"TurnID":      "string",
		"Final":       "bool",
	}

	shape := reflect.TypeOf(Event{})
	for i := range shape.NumField() {
		field := shape.Field(i)
		want, known := allowed[field.Name]
		if !known {
			t.Errorf("Event has a new field %s %s. Every consumer queues these, so anything that "+
				"can hold command output, a diff or a reply belongs in the snapshot the consumer "+
				"re-reads rather than in the notification telling it to",
				field.Name, field.Type)
			continue
		}
		if got := field.Type.String(); got != want {
			t.Errorf("Event.%s is %s, was %s", field.Name, got, want)
		}
	}

	if shape.NumField() != len(allowed) {
		t.Errorf("Event has %d fields and %d are accounted for here", shape.NumField(), len(allowed))
	}
}
