package session

import (
	"errors"
	"fmt"
	"time"
)

// OutcomeSample joins exact session cost to exact verification evidence at one revision.
//
// It lives in session rather than core because it is historical analysis, not a state transition
// the runtime depends on.
type OutcomeSample struct {
	ProjectID  string
	SessionID  string
	Revision   string
	Agent      string
	Model      string
	CostUSD    float64
	CostKnown  bool
	Passing    int
	Required   int
	ObservedAt time.Time
}

func (o OutcomeSample) validate() error {
	switch {
	case o.ProjectID == "":
		return errors.New("an outcome needs a project")
	case o.SessionID == "":
		return errors.New("an outcome needs a session")
	case o.Revision == "":
		return errors.New("an outcome needs a revision")
	case o.Model == "":
		return errors.New("an outcome needs a model")
	case o.Passing < 0 || o.Required < 1 || o.Passing > o.Required:
		return fmt.Errorf("invalid required-test outcome %d of %d", o.Passing, o.Required)
	case o.CostUSD < 0:
		return errors.New("an outcome cost cannot be negative")
	}
	return nil
}

// RecordOutcome stores or refreshes one observation.
//
// The key is project, session and revision. Re-rendering the pane is therefore idempotent, while a
// later turn on the same code may refresh its accumulated cost instead of manufacturing another
// independent sample.
func (e *Engine) RecordOutcome(sample OutcomeSample) error {
	if err := sample.validate(); err != nil {
		return err
	}
	if sample.ObservedAt.IsZero() {
		sample.ObservedAt = e.events.Now()
	}

	e.mu.Lock()
	storage := e.storage
	e.mu.Unlock()
	if storage == nil {
		return nil
	}
	return storage.saveOutcome(sample)
}

// OutcomeHistory returns exact observations for one project, oldest first.
func (e *Engine) OutcomeHistory(projectID string) ([]OutcomeSample, error) {
	if projectID == "" {
		return nil, errors.New("outcome history needs a project")
	}
	e.mu.Lock()
	storage := e.storage
	e.mu.Unlock()
	if storage == nil {
		return nil, nil
	}
	return storage.loadOutcomes(projectID)
}

func (s *Storage) saveOutcome(sample OutcomeSample) error {
	_, err := s.db.Exec(`
		INSERT INTO cost_outcomes
			(project_id, session_id, revision, agent, model, cost_usd, cost_known,
			 passing, required, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, session_id, revision) DO UPDATE SET
			agent = excluded.agent,
			model = excluded.model,
			cost_usd = excluded.cost_usd,
			cost_known = excluded.cost_known,
			passing = excluded.passing,
			required = excluded.required,
			observed_at = excluded.observed_at`,
		sample.ProjectID, sample.SessionID, sample.Revision, sample.Agent, sample.Model,
		sample.CostUSD, boolToInt(sample.CostKnown), sample.Passing, sample.Required,
		unix(sample.ObservedAt))
	if err != nil {
		return fmt.Errorf("saving the cost outcome for session %s: %w", sample.SessionID, err)
	}
	return nil
}

func (s *Storage) loadOutcomes(projectID string) ([]OutcomeSample, error) {
	rows, err := s.db.Query(`
		SELECT project_id, session_id, revision, agent, model, cost_usd, cost_known,
		       passing, required, observed_at
		FROM cost_outcomes WHERE project_id = ? ORDER BY observed_at, session_id, revision`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("loading cost outcomes for this project: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []OutcomeSample
	for rows.Next() {
		var sample OutcomeSample
		var costKnown int
		var observed int64
		if err := rows.Scan(
			&sample.ProjectID, &sample.SessionID, &sample.Revision, &sample.Agent, &sample.Model,
			&sample.CostUSD, &costKnown, &sample.Passing, &sample.Required, &observed,
		); err != nil {
			return nil, err
		}
		sample.CostKnown = costKnown != 0
		sample.ObservedAt = fromUnix(observed)
		out = append(out, sample)
	}
	return out, rows.Err()
}
