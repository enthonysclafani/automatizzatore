package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Step is one recorded step execution.
type Step struct {
	RunID      string          `json:"runId"`
	Seq        int             `json:"seq"`
	Path       string          `json:"path"`
	StepID     string          `json:"stepId"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Attempts   int             `json:"attempts"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Input      json.RawMessage `json:"input"`
	Output     json.RawMessage `json:"output"`
	Error      string          `json:"error,omitempty"`
	Logs       string          `json:"logs,omitempty"`
	CostEur    float64         `json:"costEur"`
}

// StartStep records that a step began.
func (s *Store) StartStep(ctx context.Context, runID string, seq int, path, stepID, typ string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO steps (run_id, seq, path, step_id, type, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, runID, seq, path, stepID, typ, StepRunning, ts(now))
	return err
}

// StepResult is what FinishStep records.
type StepResult struct {
	Status   string
	Attempts int
	Input    any
	Output   any
	Error    string
	Logs     string
	CostEur  float64
}

// FinishStep records the outcome of a step.
func (s *Store) FinishStep(ctx context.Context, runID string, seq int, r StepResult, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE steps SET status = ?, attempts = ?, input = ?, output = ?, error = ?, logs = ?,
		cost_eur = ?, finished_at = ? WHERE run_id = ? AND seq = ?`,
		r.Status, r.Attempts, toJSON(r.Input), toJSON(r.Output), r.Error, r.Logs, r.CostEur, ts(now), runID, seq)
	return err
}

// ListSteps returns the steps of a run in execution order.
func (s *Store) ListSteps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, seq, path, step_id, type, status, attempts, started_at, finished_at,
		input, output, error, logs, cost_eur FROM steps WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var (
			st            Step
			started       string
			finished      sql.NullString
			input, output string
		)
		if err := rows.Scan(&st.RunID, &st.Seq, &st.Path, &st.StepID, &st.Type, &st.Status, &st.Attempts, &started,
			&finished, &input, &output, &st.Error, &st.Logs, &st.CostEur); err != nil {
			return nil, err
		}
		st.StartedAt = mustTS(started)
		st.FinishedAt = parseTS(finished)
		st.Input = json.RawMessage(input)
		st.Output = json.RawMessage(output)
		out = append(out, st)
	}
	return out, rows.Err()
}

// LLMCall is one recorded request to a language model.
type LLMCall struct {
	ID           int64           `json:"id"`
	RunID        string          `json:"runId"`
	StepPath     string          `json:"stepPath"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Request      json.RawMessage `json:"request"`
	Response     string          `json:"response"`
	InputTokens  int64           `json:"inputTokens"`
	OutputTokens int64           `json:"outputTokens"`
	CostEur      float64         `json:"costEur"`
	PriceKnown   bool            `json:"priceKnown"`
	LatencyMs    int64           `json:"latencyMs"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// AddLLMCall records a model call.
func (s *Store) AddLLMCall(ctx context.Context, c LLMCall) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO llm_calls (run_id, step_path, provider, model, request, response,
		input_tokens, output_tokens, cost_eur, price_known, latency_ms, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.RunID, c.StepPath, c.Provider, c.Model, toJSON(c.Request), c.Response, c.InputTokens, c.OutputTokens,
		c.CostEur, boolInt(c.PriceKnown), c.LatencyMs, c.Error, ts(c.CreatedAt))
	return err
}

// ListLLMCalls returns the model calls of a run.
func (s *Store) ListLLMCalls(ctx context.Context, runID string) ([]LLMCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, step_path, provider, model, request, response, input_tokens,
		output_tokens, cost_eur, price_known, latency_ms, error, created_at FROM llm_calls WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LLMCall
	for rows.Next() {
		var (
			c          LLMCall
			req, at    string
			priceKnown int
		)
		if err := rows.Scan(&c.ID, &c.RunID, &c.StepPath, &c.Provider, &c.Model, &req, &c.Response, &c.InputTokens,
			&c.OutputTokens, &c.CostEur, &priceKnown, &c.LatencyMs, &c.Error, &at); err != nil {
			return nil, err
		}
		c.Request = json.RawMessage(req)
		c.PriceKnown = priceKnown == 1
		c.CreatedAt = mustTS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Automation is the stored state of an automation folder.
type Automation struct {
	Name      string          `json:"name"`
	Workspace string          `json:"workspace"`
	Path      string          `json:"path"`
	Hash      string          `json:"hash"`
	Valid     bool            `json:"valid"`
	Problems  json.RawMessage `json:"problems"`
	Enabled   bool            `json:"enabled"`
	LoadedAt  time.Time       `json:"loadedAt"`
}

// UpsertAutomation records an automation found on disk. It keeps the
// enabled flag, which people control.
func (s *Store) UpsertAutomation(ctx context.Context, a Automation, now time.Time) error {
	problems := a.Problems
	if len(problems) == 0 {
		problems = json.RawMessage("[]")
	}
	ws := workspaceOr(a.Workspace)
	if err := s.EnsureWorkspace(ctx, ws, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO automations (name, workspace, path, hash, valid, problems, loaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET workspace = excluded.workspace, path = excluded.path, hash = excluded.hash,
			valid = excluded.valid, problems = excluded.problems, loaded_at = excluded.loaded_at`,
		a.Name, ws, a.Path, a.Hash, boolInt(a.Valid), string(problems), ts(now))
	return err
}

// DeleteAutomationsExcept removes automations whose folder is gone.
func (s *Store) DeleteAutomationsExcept(ctx context.Context, keep []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT name FROM automations`)
	if err != nil {
		return err
	}
	var gone []string
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		if !keepSet[n] {
			gone = append(gone, n)
		}
	}
	_ = rows.Close()
	for _, n := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM automations WHERE name = ?`, n); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM trigger_state WHERE automation = ?`, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListAutomations returns every known automation.
func (s *Store) ListAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, workspace, path, hash, valid, problems, enabled, loaded_at FROM automations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Automation
	for rows.Next() {
		var (
			a              Automation
			valid, enabled int
			problems, at   string
		)
		if err := rows.Scan(&a.Name, &a.Workspace, &a.Path, &a.Hash, &valid, &problems, &enabled, &at); err != nil {
			return nil, err
		}
		a.Valid, a.Enabled = valid == 1, enabled == 1
		a.Problems = json.RawMessage(problems)
		a.LoadedAt = mustTS(at)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetEnabled pauses or resumes an automation.
func (s *Store) SetEnabled(ctx context.Context, name string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE automations SET enabled = ? WHERE name = ?`, boolInt(enabled), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// IsEnabled reports whether an automation may run. Unknown automations
// count as enabled.
func (s *Store) IsEnabled(ctx context.Context, name string) (bool, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM automations WHERE name = ?`, name).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return enabled == 1, err
}

// TriggerState is when a schedule fires next.
type TriggerState struct {
	NextRunAt       time.Time
	LastScheduledAt *time.Time
}

// GetTriggerState returns the state of a schedule trigger, or ErrNotFound.
func (s *Store) GetTriggerState(ctx context.Context, automation, key string) (*TriggerState, error) {
	var next string
	var last sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT next_run_at, last_scheduled_at FROM trigger_state WHERE automation = ? AND trigger_key = ?`,
		automation, key).Scan(&next, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &TriggerState{NextRunAt: mustTS(next), LastScheduledAt: parseTS(last)}, nil
}

// SetTriggerState stores the state of a schedule trigger.
func (s *Store) SetTriggerState(ctx context.Context, automation, key string, st TriggerState) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO trigger_state (automation, trigger_key, next_run_at, last_scheduled_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (automation, trigger_key) DO UPDATE SET next_run_at = excluded.next_run_at,
			last_scheduled_at = excluded.last_scheduled_at`,
		automation, key, ts(st.NextRunAt), tsPtr(st.LastScheduledAt))
	return err
}

// PruneTriggerStates removes trigger states of an automation whose keys are
// no longer in use, after a schedule edit.
func (s *Store) PruneTriggerStates(ctx context.Context, automation string, keep []string) error {
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT trigger_key FROM trigger_state WHERE automation = ?`, automation)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			_ = rows.Close()
			return err
		}
		if !keepSet[k] {
			stale = append(stale, k)
		}
	}
	_ = rows.Close()
	for _, k := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM trigger_state WHERE automation = ? AND trigger_key = ?`, automation, k); err != nil {
			return err
		}
	}
	return nil
}

// AcquireLock takes or renews the named lease for owner. It reports false
// when someone else holds an unexpired lease.
func (s *Store) AcquireLock(ctx context.Context, name, owner string, ttl time.Duration, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO locks (name, owner, until) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET owner = excluded.owner, until = excluded.until
		WHERE locks.owner = excluded.owner OR locks.until < ?`,
		name, owner, ts(now.Add(ttl)), ts(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseLock gives up a lease held by owner.
func (s *Store) ReleaseLock(ctx context.Context, name, owner string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM locks WHERE name = ? AND owner = ?`, name, owner)
	return err
}
