package session

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func testStorage(t *testing.T) *Storage {
	t.Helper()
	storage, err := OpenStorage(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

var storedAt = time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

func storedTurn(id, ask, reply string, state core.TurnState) core.Turn {
	turn := core.Turn{
		ID:        id,
		State:     state,
		Request:   core.Message{Role: core.RoleUser, Text: ask},
		Text:      reply,
		Usage:     core.Usage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01, CostKnown: true},
		Provider:  "claude",
		Model:     "claude-opus-5",
		StartedAt: storedAt,
	}
	if state.Terminal() {
		turn.EndedAt = storedAt.Add(2 * time.Second)
	}
	return turn
}

// A conversation that comes back different from how it went in is worse than one that was not saved
// at all, because the difference is invisible until somebody relies on it.
func TestASessionSurvivesARoundTrip(t *testing.T) {
	storage := testStorage(t)

	original := core.Session{
		ID:          "session-1",
		Title:       "add a login form",
		WorkspaceID: "ws-1",
		KeyName:     "claude",
		Model:       "claude-opus-5",
		CreatedAt:   storedAt,
		UpdatedAt:   storedAt.Add(time.Minute),
		Turns: []core.Turn{
			storedTurn("t1", "what is 2+2", "four", core.TurnComplete),
			func() core.Turn {
				turn := storedTurn("t2", "read the file", "let me look", core.TurnComplete)
				turn.Thinking = "the user wants a file read"
				turn.ToolCalls = []core.ToolCall{
					{ID: "c1", Name: "read", Input: []byte(`{"path":"main.go"}`)},
				}
				turn.ToolResults = []core.ToolResult{
					{CallID: "c1", Content: "permission denied", IsError: true},
				}
				return turn
			}(),
		},
	}

	if err := storage.SaveSession(original); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	for i, turn := range original.Turns {
		if err := storage.SaveTurn(original.ID, i, turn); err != nil {
			t.Fatalf("SaveTurn: %v", err)
		}
	}

	loaded, err := storage.Load(original.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Title != original.Title || loaded.KeyName != original.KeyName {
		t.Errorf("session = %+v", loaded)
	}
	if !loaded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("created at = %v, want %v", loaded.CreatedAt, original.CreatedAt)
	}
	if len(loaded.Turns) != 2 {
		t.Fatalf("%d turns, want 2", len(loaded.Turns))
	}
	if loaded.Turns[0].Text != "four" {
		t.Errorf("first reply = %q", loaded.Turns[0].Text)
	}

	// The tool result's error flag is the field most likely to be lost in a round trip, and losing
	// it turns a refused tool call into one the model reads as having succeeded.
	results := loaded.Turns[1].ToolResults
	if len(results) != 1 || !results[0].IsError {
		t.Errorf("the tool result lost its error flag: %+v", results)
	}
	if len(loaded.Turns[1].ToolCalls) != 1 ||
		string(loaded.Turns[1].ToolCalls[0].Input) != `{"path":"main.go"}` {
		t.Errorf("tool calls = %+v", loaded.Turns[1].ToolCalls)
	}
	if loaded.Turns[0].Usage.CostUSD != 0.01 || !loaded.Turns[0].Usage.CostKnown {
		t.Errorf("usage = %+v, want the cost kept", loaded.Turns[0].Usage)
	}

	// And the whole thing has to be a session the rest of the program will accept.
	if err := loaded.Validate(); err != nil {
		t.Errorf("a loaded session is not valid: %v", err)
	}
}

// A turn that was in flight when the process died is not in flight now, and nothing is going to
// finish it. Left as streaming it would spin forever on screen and make the session invalid.
func TestAnUnfinishedTurnComesBackAsInterrupted(t *testing.T) {
	storage := testStorage(t)

	session := core.Session{ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt}
	if err := storage.SaveSession(session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	inFlight := core.Turn{
		ID:        "t1",
		State:     core.TurnStreaming,
		Request:   core.Message{Role: core.RoleUser, Text: "count to a million"},
		Text:      "one, two, thr",
		StartedAt: storedAt,
	}
	if err := storage.SaveTurn(session.ID, 0, inFlight); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	loaded, err := storage.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	turn := loaded.Turns[0]

	if turn.State != core.TurnInterrupted {
		t.Errorf("state = %s, want interrupted", turn.State)
	}
	if turn.Text != "one, two, thr" {
		t.Errorf("the partial reply was lost: %q", turn.Text)
	}
	if turn.EndedAt.IsZero() {
		t.Error("a terminal turn with no end time counts up forever on screen")
	}
	if err := loaded.Validate(); err != nil {
		t.Errorf("a recovered session must be valid: %v", err)
	}
}

// The guarantee: killing the process mid turn loses at most the turn in flight.
func TestSavingATurnTwiceKeepsTheLatest(t *testing.T) {
	storage := testStorage(t)

	session := core.Session{ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt}
	if err := storage.SaveSession(session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// As it starts, which is what puts the question beyond reach of a crash.
	pending := core.Turn{
		ID: "t1", State: core.TurnPending,
		Request:   core.Message{Role: core.RoleUser, Text: "the question"},
		StartedAt: storedAt,
	}
	if err := storage.SaveTurn(session.ID, 0, pending); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	// And again as it ends.
	done := pending
	done.State = core.TurnComplete
	done.Text = "the answer"
	done.EndedAt = storedAt.Add(time.Second)
	if err := storage.SaveTurn(session.ID, 0, done); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	loaded, err := storage.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Turns) != 1 {
		t.Fatalf("%d turns, want the second save to have replaced the first", len(loaded.Turns))
	}
	if loaded.Turns[0].Text != "the answer" || loaded.Turns[0].State != core.TurnComplete {
		t.Errorf("turn = %+v", loaded.Turns[0])
	}
}

func TestSearchFindsAMessageAcrossSessions(t *testing.T) {
	storage := testStorage(t)

	for i, spec := range []struct{ id, title, ask, reply string }{
		{"session-1", "auth work", "how do I hash a password", "use bcrypt with a cost of 12"},
		{"session-2", "layout work", "why is my footer floating", "pin it with padding"},
	} {
		s := core.Session{ID: spec.id, Title: spec.title, CreatedAt: storedAt, UpdatedAt: storedAt}
		if err := storage.SaveSession(s); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
		if err := storage.SaveTurn(spec.id, i,
			storedTurn("t1", spec.ask, spec.reply, core.TurnComplete)); err != nil {
			t.Fatalf("SaveTurn: %v", err)
		}
	}

	hits, err := storage.Search("bcrypt", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("%d hits, want 1: %+v", len(hits), hits)
	}
	if hits[0].SessionID != "session-1" || hits[0].SessionTitle != "auth work" {
		t.Errorf("hit = %+v, want the session named so somebody can go there", hits[0])
	}
	if !strings.Contains(hits[0].Excerpt, "bcrypt") {
		t.Errorf("excerpt = %q, want the match visible in it", hits[0].Excerpt)
	}

	// The question is searchable too, not only the answer. People remember what they asked more
	// often than what they were told.
	hits, err = storage.Search("hash a password", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Error("searching for the question found nothing")
	}
}

// FTS5 has an operator syntax, so a search for a Go error message is otherwise a syntax error
// rather than a search, and the user sees a crash for typing a colon.
func TestSearchAcceptsAwkwardInput(t *testing.T) {
	storage := testStorage(t)

	s := core.Session{ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt}
	if err := storage.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := storage.SaveTurn("session-1", 0, storedTurn("t1",
		"what is this", `cannot use x (type int) as type string`, core.TurnComplete)); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	for _, query := range []string{
		`cannot use x (type int)`,
		`"quoted"`,
		`a AND b OR NOT c`,
		`*`,
		`:`,
	} {
		if _, err := storage.Search(query, 10); err != nil {
			t.Errorf("searching for %q failed rather than finding nothing: %v", query, err)
		}
	}
}

// A search that keeps returning a conversation the user deleted is the kind of thing people notice
// and do not forgive.
func TestDeletingASessionRemovesItFromSearch(t *testing.T) {
	storage := testStorage(t)

	s := core.Session{ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt}
	if err := storage.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := storage.SaveTurn("session-1", 0,
		storedTurn("t1", "ask", "a distinctive vermilion answer", core.TurnComplete)); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	if hits, _ := storage.Search("vermilion", 10); len(hits) != 1 {
		t.Fatalf("precondition: %d hits", len(hits))
	}

	if err := storage.Delete("session-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	hits, err := storage.Search("vermilion", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("%d hits after deletion, want none: %+v", len(hits), hits)
	}
	if _, err := storage.Load("session-1"); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session is still loadable after deletion: %v", err)
	}
}

// Reopening is the normal case, and a migration that ran twice would fail on the second table
// creation and lock somebody out of their own history.
func TestReopeningAnExistingFileWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	first, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if err := first.SaveSession(core.Session{
		ID: "session-1", Title: "kept", CreatedAt: storedAt, UpdatedAt: storedAt,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = second.Close() }()

	loaded, err := second.Load("session-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "kept" {
		t.Errorf("title = %q", loaded.Title)
	}
}

// Running an older schema over a newer file silently drops whatever the newer one added, and the
// user finds out when their history has holes in it.
func TestANewerSchemaIsRefusedRatherThanDowngraded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if _, err := storage.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatalf("bumping the version: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = OpenStorage(path)
	if err == nil {
		t.Fatal("a file from a newer Canopy should be refused, not silently downgraded")
	}
	if !strings.Contains(err.Error(), "newer version") {
		t.Errorf("the error should say what happened and what to do, got %q", err)
	}
}

// A turn with no end time read back as ending at the Unix epoch would report a duration of fifty
// six years, which looks like a bug in the timer rather than an unfinished turn.
func TestTheZeroTimeStaysZero(t *testing.T) {
	storage := testStorage(t)

	if err := storage.SaveSession(core.Session{
		ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := storage.SaveTurn("session-1", 0, core.Turn{
		ID: "t1", State: core.TurnComplete,
		Request:   core.Message{Role: core.RoleUser, Text: "ask"},
		StartedAt: storedAt,
		EndedAt:   storedAt,
	}); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}

	loaded, err := storage.Load("session-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Turns[0].EndedAt.Equal(storedAt) {
		t.Errorf("end time = %v, want %v", loaded.Turns[0].EndedAt, storedAt)
	}
}

// The engine keeps working when there is nowhere to save to. A storage failure taking the
// conversation down with it would be the tail wagging the dog.
func TestAnEngineWithoutStorageStillWorks(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("no disk needed")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	s := e.Create("claude", "m")
	turnID, err := e.Send(s.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn := waitForTurn(t, e, s.ID, turnID); turn.Text != "no disk needed" {
		t.Errorf("text = %q", turn.Text)
	}
}

// The point of persistence: quit, come back, and the conversation is where you left it.
func TestAnEngineResumesWhatItSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}

	client := &scriptedClient{name: "claude", events: reply("bcrypt with a cost of 12")}
	first := New(fixedResolver{client: client, id: anthropicID()})
	if err := first.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	s := first.Create("claude", "claude-opus-5")
	turnID, err := first.Send(s.ID, "how do I hash a password")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, first, s.ID, turnID)

	// Closing the engine closes the storage it was given, because WithStorage hands ownership over.
	// Two owners of one handle is how a file gets closed while something is still writing to it.
	first.Close()

	// A second run, as though the program had been quit and started again.
	reopened, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}

	second := New(fixedResolver{client: client, id: anthropicID()})
	defer second.Close()
	if err := second.WithStorage(reopened, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	sessions := second.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("%d sessions after resuming, want 1", len(sessions))
	}
	if len(sessions[0].Turns) != 1 || sessions[0].Turns[0].Text != "bcrypt with a cost of 12" {
		t.Errorf("the conversation did not come back: %+v", sessions[0].Turns)
	}
	if sessions[0].Title != "how do I hash a password" {
		t.Errorf("title = %q", sessions[0].Title)
	}

	// And a new session must not reuse the ID of the one already on disk, or tonight's turns get
	// appended to last week's conversation.
	fresh := second.Create("claude", "claude-opus-5")
	if fresh.ID == sessions[0].ID {
		t.Errorf("a new session took the existing ID %q, so it would append to it", fresh.ID)
	}
}

// Undo working until you quit and then silently stopping is worse than not offering it.
func TestACheckpointSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if err := storage.SaveSession(core.Session{
		ID: "session-1", CreatedAt: storedAt, UpdatedAt: storedAt,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	turn := storedTurn("t1", "change it", "changed", core.TurnComplete)
	turn.Checkpoint = "abc123def456"
	if err := storage.SaveTurn("session-1", 0, turn); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	loaded, err := reopened.Load("session-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Turns[0].Checkpoint != "abc123def456" {
		t.Errorf("the checkpoint was lost on restart: %q", loaded.Turns[0].Checkpoint)
	}
}

// M-03. The task list is most of what makes a long run followable, and a long run is exactly the
// kind you close the laptop on. Without persisting it the list would be complete right up until you
// quit and then come back empty, which is worse than never showing one.
func TestATaskListSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	first, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	saved := core.Session{
		ID:        "s1",
		CreatedAt: storedAt,
		UpdatedAt: storedAt,
		Tasks: []core.Task{
			{Text: "read the poller", State: core.TaskDone, Outcome: "it polls every two seconds"},
			{Text: "add the flag", State: core.TaskInProgress},
		},
	}
	if err := first.SaveSession(saved); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = second.Close() }()

	loaded, err := second.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Tasks) != 2 {
		t.Fatalf("%d tasks survived, want 2: %+v", len(loaded.Tasks), loaded.Tasks)
	}
	if loaded.Tasks[0].Outcome != "it polls every two seconds" {
		t.Errorf("the outcome did not survive: %q", loaded.Tasks[0].Outcome)
	}
	if loaded.Tasks[1].State != core.TaskInProgress {
		t.Errorf("the state did not survive: %q", loaded.Tasks[1].State)
	}
}

// A session written before the task list existed has no value in that column, and opening it must
// not fail. The migration gives it a default; this is the test that says so.
func TestASessionWithNoTasksLoadsCleanly(t *testing.T) {
	storage := testStorage(t)

	if err := storage.SaveSession(core.Session{
		ID: "s1", CreatedAt: storedAt, UpdatedAt: storedAt,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := storage.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Tasks) != 0 {
		t.Errorf("a session with no tasks came back with %+v", loaded.Tasks)
	}
}

// A task list is a display. Refusing to open a conversation because its progress summary is
// malformed would trade the conversation for the summary of it.
func TestAMalformedTaskListDoesNotBlockTheConversation(t *testing.T) {
	storage := testStorage(t)

	if err := storage.SaveSession(core.Session{
		ID: "s1", CreatedAt: storedAt, UpdatedAt: storedAt,
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := storage.db.Exec(`UPDATE sessions SET tasks = ? WHERE id = ?`,
		"{not json at all", "s1"); err != nil {
		t.Fatalf("corrupting the column: %v", err)
	}

	loaded, err := storage.Load("s1")
	if err != nil {
		t.Fatalf("a conversation would not open because its task list was malformed: %v", err)
	}
	if loaded.ID != "s1" {
		t.Errorf("the conversation loaded as %q", loaded.ID)
	}
	if len(loaded.Tasks) != 0 {
		t.Errorf("a malformed list came back as %+v", loaded.Tasks)
	}
}

// A history file from the build before this one has to come forward without anybody noticing, which
// is the whole reason migrations were in the first version rather than added when they were needed.
func TestAFileAtTheOlderSchemaMigratesForward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	// A file at version seven: every migration but the last, which is where the asides table came
	// from. Built by applying them rather than by shipping a fixture, so this keeps testing the real
	// path as more are added.
	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if err := storage.SaveSession(core.Session{ID: "s1", Title: "written by the older build"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := storage.db.Exec(`DROP TABLE asides; PRAGMA user_version = 7`); err != nil {
		t.Fatalf("winding the file back: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	forward, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("opening a version seven file: %v", err)
	}
	defer func() { _ = forward.Close() }()

	var version int
	if err := forward.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("reading the version: %v", err)
	}
	if version != schemaVersion {
		t.Errorf("the file is at schema %d, want %d", version, schemaVersion)
	}

	// Nothing that was there before is gone, and the thing that was added works.
	loaded, err := forward.Load("s1")
	if err != nil {
		t.Fatalf("Load after migrating: %v", err)
	}
	if loaded.Title != "written by the older build" {
		t.Errorf("the conversation came back as %q", loaded.Title)
	}
	if err := forward.saveAside("s1", Aside{
		Question: "does it still work", Answer: "yes", At: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("saving an aside after migrating: %v", err)
	}
	if kept, err := forward.loadAsides("s1"); err != nil || len(kept) != 1 {
		t.Errorf("asides after migrating: %+v, %v", kept, err)
	}
}

// An aside belongs to its conversation and goes when the conversation goes, through the same foreign
// key everything else hanging off a session goes through.
func TestDeletingAConversationTakesItsAsidesWithIt(t *testing.T) {
	storage := testStorage(t)

	if err := storage.SaveSession(core.Session{ID: "s1", Title: "temporary"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := storage.saveAside("s1", Aside{
		Question: "why", Answer: "because", At: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("saveAside: %v", err)
	}
	if err := storage.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	kept, err := storage.loadAsides("s1")
	if err != nil {
		t.Fatalf("loadAsides: %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("a deleted conversation left %+v behind", kept)
	}
}
