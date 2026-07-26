package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Pure Go, so Canopy cross compiles and installs without a C toolchain. The cgo driver is
	// smaller to download and would make `go install github.com/.../canopy` fail on any machine
	// without a compiler, which is most of them.
	_ "modernc.org/sqlite"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Storage persists sessions to SQLite.
//
// Sessions, the audit trail, cost history and run reports are all queries over the same rows, so
// one storage decision buys four features. See D-24.
//
// What gets written and when is the part worth understanding. A turn is saved twice: once when it
// starts, so the question survives a crash, and once when it reaches a terminal state, so the answer
// does. Nothing is written per token. Writing on every token would turn a streamed reply into
// thousands of transactions, and the guarantee that buys is not one anybody asked for: losing the
// last few words of a reply that was still arriving when the process died is not a loss worth
// paying for on every keystroke of every agent.
type Storage struct {
	db *sql.DB
}

// schemaVersion is the migration this build expects. See migrations.
const schemaVersion = 4

// migrations are applied in order, and the file records how far it has got in `PRAGMA user_version`.
//
// Present from the first version rather than added when the schema first changes, because the
// alternative is discovering you need them while holding somebody's history in a shape you cannot
// read. A tool that loses your conversations on upgrade is not one anyone keeps.
var migrations = []string{
	`
	CREATE TABLE sessions (
		id           TEXT PRIMARY KEY,
		title        TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		key_name     TEXT NOT NULL DEFAULT '',
		model        TEXT NOT NULL DEFAULT '',
		created_at   INTEGER NOT NULL,
		updated_at   INTEGER NOT NULL
	);

	CREATE TABLE turns (
		rowid        INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		turn_id      TEXT NOT NULL,
		ordinal      INTEGER NOT NULL,
		state        TEXT NOT NULL,
		request      TEXT NOT NULL,
		request_text TEXT NOT NULL DEFAULT '',
		reply        TEXT NOT NULL DEFAULT '',
		thinking     TEXT NOT NULL DEFAULT '',
		tool_calls   TEXT NOT NULL DEFAULT '[]',
		tool_results TEXT NOT NULL DEFAULT '[]',
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read    INTEGER NOT NULL DEFAULT 0,
		cache_write   INTEGER NOT NULL DEFAULT 0,
		cost_usd      REAL    NOT NULL DEFAULT 0,
		cost_known    INTEGER NOT NULL DEFAULT 0,
		provider     TEXT NOT NULL DEFAULT '',
		model        TEXT NOT NULL DEFAULT '',
		error        TEXT NOT NULL DEFAULT '',
		started_at   INTEGER NOT NULL DEFAULT 0,
		ended_at     INTEGER NOT NULL DEFAULT 0,
		UNIQUE (session_id, turn_id)
	);

	CREATE INDEX turns_by_session ON turns (session_id, ordinal);

	-- An external content index, so the text is stored once rather than twice. The triggers below
	-- are what keep it honest: without them a deleted turn stays findable and a search returns rows
	-- that no longer exist.
	CREATE VIRTUAL TABLE turns_fts USING fts5(
		request_text, reply,
		content='turns', content_rowid='rowid'
	);

	CREATE TRIGGER turns_after_insert AFTER INSERT ON turns BEGIN
		INSERT INTO turns_fts(rowid, request_text, reply)
		VALUES (new.rowid, new.request_text, new.reply);
	END;

	CREATE TRIGGER turns_after_delete AFTER DELETE ON turns BEGIN
		INSERT INTO turns_fts(turns_fts, rowid, request_text, reply)
		VALUES ('delete', old.rowid, old.request_text, old.reply);
	END;

	CREATE TRIGGER turns_after_update AFTER UPDATE ON turns BEGIN
		INSERT INTO turns_fts(turns_fts, rowid, request_text, reply)
		VALUES ('delete', old.rowid, old.request_text, old.reply);
		INSERT INTO turns_fts(rowid, request_text, reply)
		VALUES (new.rowid, new.request_text, new.reply);
	END;
	`,

	// Added when compaction landed. A second migration rather than an edit to the first, because
	// somebody already has a file at version one and editing a migration that has run changes
	// nothing for them while making the two disagree.
	`
	CREATE TABLE compactions (
		session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		ordinal       INTEGER NOT NULL,
		summary       TEXT NOT NULL,
		through       INTEGER NOT NULL,
		at            INTEGER NOT NULL,
		tokens_before INTEGER NOT NULL DEFAULT 0,
		tokens_after  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_id, ordinal)
	);
	`,

	// Added when forking landed.
	`
	ALTER TABLE sessions ADD COLUMN forked_from TEXT NOT NULL DEFAULT '';
	ALTER TABLE sessions ADD COLUMN forked_at   TEXT NOT NULL DEFAULT '';
	ALTER TABLE sessions ADD COLUMN forked_when INTEGER NOT NULL DEFAULT 0;

	-- The child could be derived by querying sessions for a matching forked_from, and is stored
	-- explicitly anyway. A fork whose child has been deleted should still show that something was
	-- tried from here and is gone, rather than silently reading as though it never happened.
	CREATE TABLE forks (
		session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		fork_id     TEXT NOT NULL,
		at_turn_id  TEXT NOT NULL,
		at          INTEGER NOT NULL,
		PRIMARY KEY (session_id, fork_id)
	);
	`,

	// Added when checkpoints landed. Without persisting this, undo would work until you quit and
	// then silently stop, which is worse than not offering it.
	`ALTER TABLE turns ADD COLUMN checkpoint TEXT NOT NULL DEFAULT '';`,
}

// OpenStorage opens or creates the session database.
func OpenStorage(path string) (*Storage, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening the session store: %w", err)
	}

	// One connection. SQLite handles concurrent readers well and concurrent writers by returning
	// "database is locked", and the pool would produce exactly that under two agents finishing turns
	// at once. Serialising here costs nothing at this scale and removes a class of intermittent
	// failure that is miserable to reproduce.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		// Write ahead logging, so a crash mid write leaves the file readable and a reader is never
		// blocked by a writer. The second half matters once several agents are running.
		`PRAGMA journal_mode = WAL`,
		// Normal rather than full. With WAL this is durable against the process dying, which is the
		// guarantee that was asked for, and it avoids an fsync per turn. Full would additionally
		// survive the machine losing power mid write, which is not worth the cost here.
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configuring the session store (%s): %w", pragma, err)
		}
	}

	s := &Storage{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate brings the file up to the schema this build expects.
func (s *Storage) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading the schema version: %w", err)
	}

	if version > schemaVersion {
		// A newer Canopy has already touched this file. Refusing is the only safe answer: running
		// an older schema over a newer file silently drops whatever the newer one added, and the
		// user finds out when their history has holes in it.
		return fmt.Errorf(
			"this history was written by a newer version of Canopy (schema %d, this build "+
				"understands %d). Upgrade rather than downgrade, or the newer data would be lost",
			version, schemaVersion)
	}

	for version < len(migrations) {
		// Each migration and its version bump go in one transaction, so a failure halfway leaves
		// the file at the version it was, rather than at a version whose schema was never finished.
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("applying migration %d: %w", version+1, err)
		}
		version++
		// PRAGMA does not accept a bound parameter, and the value is a loop counter rather than
		// anything a user supplied.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the database.
func (s *Storage) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SaveSession writes a session's own details, without its turns.
func (s *Storage) SaveSession(session core.Session) error {
	if session.ID == "" {
		return errors.New("a session needs an ID to be saved")
	}
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, title, workspace_id, key_name, model, created_at, updated_at,
		                      forked_from, forked_at, forked_when)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			workspace_id = excluded.workspace_id,
			key_name = excluded.key_name,
			model = excluded.model,
			updated_at = excluded.updated_at`,
		session.ID, session.Title, session.WorkspaceID, session.KeyName, session.Model,
		unix(session.CreatedAt), unix(session.UpdatedAt),
		session.ForkedFrom, session.ForkedAt, unix(session.ForkedWhen))
	if err != nil {
		return fmt.Errorf("saving session %s: %w", session.ID, err)
	}
	if err := s.saveCompactions(session); err != nil {
		return err
	}
	return s.saveForks(session)
}

func (s *Storage) saveForks(session core.Session) error {
	for _, fork := range session.Forks {
		if _, err := s.db.Exec(`
			INSERT INTO forks (session_id, fork_id, at_turn_id, at) VALUES (?, ?, ?, ?)
			ON CONFLICT(session_id, fork_id) DO NOTHING`,
			session.ID, fork.SessionID, fork.AtTurnID, unix(fork.At)); err != nil {
			return fmt.Errorf("saving fork %s of session %s: %w", fork.SessionID, session.ID, err)
		}
	}
	return nil
}

func (s *Storage) loadForks(sessionID string) ([]core.ForkRef, error) {
	rows, err := s.db.Query(`
		SELECT fork_id, at_turn_id, at FROM forks WHERE session_id = ? ORDER BY at`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading the forks of session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.ForkRef
	for rows.Next() {
		var fork core.ForkRef
		var at int64
		if err := rows.Scan(&fork.SessionID, &fork.AtTurnID, &at); err != nil {
			return nil, err
		}
		fork.At = fromUnix(at)
		out = append(out, fork)
	}
	return out, rows.Err()
}

// saveCompactions writes the summarisation history.
//
// Written whole each time rather than appended, because the list only ever grows and rewriting a
// handful of rows is simpler than tracking which of them are already there. Simpler in a way that
// matters: the failure mode of the clever version is a duplicated summary in somebody's context.
func (s *Storage) saveCompactions(session core.Session) error {
	if len(session.Compactions) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM compactions WHERE session_id = ?`, session.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for i, compaction := range session.Compactions {
		if _, err := tx.Exec(`
			INSERT INTO compactions
				(session_id, ordinal, summary, through, at, tokens_before, tokens_after)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			session.ID, i, compaction.Summary, compaction.Through, unix(compaction.At),
			compaction.TokensBefore, compaction.TokensAfter); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("saving compaction %d of session %s: %w", i, session.ID, err)
		}
	}
	return tx.Commit()
}

func (s *Storage) loadCompactions(sessionID string) ([]core.Compaction, error) {
	rows, err := s.db.Query(`
		SELECT summary, through, at, tokens_before, tokens_after
		FROM compactions WHERE session_id = ? ORDER BY ordinal`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading the compactions of session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.Compaction
	for rows.Next() {
		var c core.Compaction
		var at int64
		if err := rows.Scan(&c.Summary, &c.Through, &at, &c.TokensBefore, &c.TokensAfter); err != nil {
			return nil, err
		}
		c.At = fromUnix(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveTurn writes one turn, replacing any earlier version of it.
//
// Called when a turn starts and again when it ends, which is what makes the guarantee true: the
// question is on disk before the answer is asked for, so a crash mid reply loses the reply and not
// the request.
func (s *Storage) SaveTurn(sessionID string, ordinal int, turn core.Turn) error {
	request, err := json.Marshal(turn.Request)
	if err != nil {
		return fmt.Errorf("encoding the request of turn %s: %w", turn.ID, err)
	}
	calls, err := json.Marshal(turn.ToolCalls)
	if err != nil {
		return fmt.Errorf("encoding the tool calls of turn %s: %w", turn.ID, err)
	}
	results, err := json.Marshal(turn.ToolResults)
	if err != nil {
		return fmt.Errorf("encoding the tool results of turn %s: %w", turn.ID, err)
	}

	_, err = s.db.Exec(`
		INSERT INTO turns (
			session_id, turn_id, ordinal, state, request, request_text, reply, thinking,
			tool_calls, tool_results,
			input_tokens, output_tokens, cache_read, cache_write, cost_usd, cost_known,
			provider, model, error, started_at, ended_at, checkpoint
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, turn_id) DO UPDATE SET
			state = excluded.state,
			reply = excluded.reply,
			thinking = excluded.thinking,
			tool_calls = excluded.tool_calls,
			tool_results = excluded.tool_results,
			input_tokens = excluded.input_tokens,
			output_tokens = excluded.output_tokens,
			cache_read = excluded.cache_read,
			cache_write = excluded.cache_write,
			cost_usd = excluded.cost_usd,
			cost_known = excluded.cost_known,
			provider = excluded.provider,
			model = excluded.model,
			error = excluded.error,
			ended_at = excluded.ended_at,
			checkpoint = excluded.checkpoint`,
		sessionID, turn.ID, ordinal, string(turn.State), string(request), turn.Request.Text,
		turn.Text, turn.Thinking, string(calls), string(results),
		turn.Usage.InputTokens, turn.Usage.OutputTokens,
		turn.Usage.CacheReadTokens, turn.Usage.CacheWriteTokens,
		turn.Usage.CostUSD, boolToInt(turn.Usage.CostKnown),
		turn.Provider, turn.Model, turn.Error, unix(turn.StartedAt), unix(turn.EndedAt),
		turn.Checkpoint)
	if err != nil {
		return fmt.Errorf("saving turn %s: %w", turn.ID, err)
	}
	return nil
}

// ErrNoSession is returned when a session ID is not in storage.
var ErrNoSession = errors.New("no such session")

// Load reads a session and all of its turns.
func (s *Storage) Load(id string) (core.Session, error) {
	var session core.Session
	var created, updated int64

	var forkedWhen int64
	err := s.db.QueryRow(`
		SELECT id, title, workspace_id, key_name, model, created_at, updated_at,
		       forked_from, forked_at, forked_when
		FROM sessions WHERE id = ?`, id).
		Scan(&session.ID, &session.Title, &session.WorkspaceID, &session.KeyName, &session.Model,
			&created, &updated, &session.ForkedFrom, &session.ForkedAt, &forkedWhen)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Session{}, fmt.Errorf("%q: %w", id, ErrNoSession)
	}
	if err != nil {
		return core.Session{}, fmt.Errorf("loading session %s: %w", id, err)
	}
	session.CreatedAt = fromUnix(created)
	session.UpdatedAt = fromUnix(updated)
	session.ForkedWhen = fromUnix(forkedWhen)

	turns, err := s.loadTurns(id)
	if err != nil {
		return core.Session{}, err
	}
	session.Turns = turns

	compactions, err := s.loadCompactions(id)
	if err != nil {
		return core.Session{}, err
	}
	session.Compactions = compactions

	forks, err := s.loadForks(id)
	if err != nil {
		return core.Session{}, err
	}
	session.Forks = forks
	return session, nil
}

func (s *Storage) loadTurns(sessionID string) ([]core.Turn, error) {
	rows, err := s.db.Query(`
		SELECT turn_id, state, request, reply, thinking, tool_calls, tool_results,
		       input_tokens, output_tokens, cache_read, cache_write, cost_usd, cost_known,
		       provider, model, error, started_at, ended_at, checkpoint
		FROM turns WHERE session_id = ? ORDER BY ordinal`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading the turns of session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	var turns []core.Turn
	for rows.Next() {
		var t core.Turn
		var state, request, calls, results string
		var costKnown int
		var started, ended int64

		if err := rows.Scan(&t.ID, &state, &request, &t.Text, &t.Thinking, &calls, &results,
			&t.Usage.InputTokens, &t.Usage.OutputTokens,
			&t.Usage.CacheReadTokens, &t.Usage.CacheWriteTokens,
			&t.Usage.CostUSD, &costKnown,
			&t.Provider, &t.Model, &t.Error, &started, &ended, &t.Checkpoint); err != nil {
			return nil, err
		}

		t.State = core.TurnState(state)
		t.Usage.CostKnown = costKnown != 0
		t.StartedAt = fromUnix(started)
		t.EndedAt = fromUnix(ended)

		if err := json.Unmarshal([]byte(request), &t.Request); err != nil {
			return nil, fmt.Errorf("decoding the request of turn %s: %w", t.ID, err)
		}
		if err := json.Unmarshal([]byte(calls), &t.ToolCalls); err != nil {
			return nil, fmt.Errorf("decoding the tool calls of turn %s: %w", t.ID, err)
		}
		if err := json.Unmarshal([]byte(results), &t.ToolResults); err != nil {
			return nil, fmt.Errorf("decoding the tool results of turn %s: %w", t.ID, err)
		}

		// A turn that was in flight when the process died is not in flight now, and nothing is
		// going to finish it. Left as streaming it would show as running forever, and
		// Session.Validate would reject the session for having an unfinished turn.
		if !t.State.Terminal() {
			t.State = core.TurnInterrupted
			if t.EndedAt.IsZero() {
				t.EndedAt = t.StartedAt
			}
			if t.Text == "" && t.Error == "" {
				t.Error = "Canopy stopped while this turn was still running"
			}
		}

		turns = append(turns, t)
	}
	return turns, rows.Err()
}

// List returns every session, most recently active first, without their turns.
//
// Without their turns on purpose: a session list shows titles and timestamps, and loading every
// message of every conversation to draw it would make the list slower the longer you have used the
// program.
func (s *Storage) List() ([]core.Session, error) {
	rows, err := s.db.Query(`
		SELECT id, title, workspace_id, key_name, model, created_at, updated_at
		FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.Session
	for rows.Next() {
		var session core.Session
		var created, updated int64
		if err := rows.Scan(&session.ID, &session.Title, &session.WorkspaceID,
			&session.KeyName, &session.Model, &created, &updated); err != nil {
			return nil, err
		}
		session.CreatedAt = fromUnix(created)
		session.UpdatedAt = fromUnix(updated)
		out = append(out, session)
	}
	return out, rows.Err()
}

// SearchHit is one match from a history search.
type SearchHit struct {
	SessionID    string
	SessionTitle string
	TurnID       string
	// Excerpt is the matching text with the query terms marked by the double angle quotes SQLite's
	// snippet function inserts. Kept as markers rather than styled here, because this package has
	// no business knowing how the interface renders emphasis.
	Excerpt string
	At      time.Time
}

// Search finds turns matching a full text query across every session.
func (s *Storage) Search(query string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`
		SELECT t.session_id, s.title, t.turn_id,
		       snippet(turns_fts, -1, '<<', '>>', ' ... ', 12),
		       t.started_at
		FROM turns_fts
		JOIN turns t ON t.rowid = turns_fts.rowid
		JOIN sessions s ON s.id = t.session_id
		WHERE turns_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, escapeFTS(query), limit)
	if err != nil {
		return nil, fmt.Errorf("searching history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var at int64
		if err := rows.Scan(&hit.SessionID, &hit.SessionTitle, &hit.TurnID, &hit.Excerpt, &at); err != nil {
			return nil, err
		}
		hit.At = fromUnix(at)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// escapeFTS turns a user's words into a query FTS5 will accept.
//
// FTS5's syntax has operators, and a search for a Go error message containing a colon or a quote is
// a syntax error rather than a search. Quoting each word and joining them makes every input a
// literal phrase search, which is what somebody typing words into a search box means.
func escapeFTS(query string) string {
	fields := strings.Fields(query)
	quoted := make([]string, 0, len(fields))
	for _, field := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(field, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// Delete removes a session and everything in it.
func (s *Storage) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting session %s: %w", id, err)
	}
	// The turns go with it through the foreign key, and their search entries go with those through
	// the delete trigger. Worth saying out loud, because a search that keeps returning a
	// conversation the user deleted is the kind of thing people notice and do not forgive.
	return nil
}

// unix stores a time as nanoseconds, or zero for the zero time.
//
// Zero rather than the Unix epoch, because "this turn has not ended" and "this turn ended in 1970"
// need to be different values. A turn read back with an epoch end time would report a duration of
// fifty six years.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PathEnvVar overrides where history is kept.
//
// Exists so the storage path can be exercised without writing into somebody's real configuration
// directory, and because a user with an encrypted volume or a synced home directory has a
// legitimate reason to put their conversations somewhere specific.
const PathEnvVar = "CANOPY_HISTORY"

// DefaultPath is where history lives.
//
// Beside the credentials rather than in a data directory of its own, because a user who wants to
// know what Canopy has put on their machine should find all of it in one place. `canopy` is a tool
// people try out, and one that scatters files is one they cannot cleanly remove.
func DefaultPath() (string, error) {
	if override := os.Getenv(PathEnvVar); override != "" {
		if dir := filepath.Dir(override); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", fmt.Errorf("creating %s: %w", dir, err)
			}
		}
		return override, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the configuration directory: %w", err)
	}
	dir := filepath.Join(base, "canopy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, "history.db"), nil
}
