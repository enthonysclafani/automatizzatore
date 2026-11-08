package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Run is one execution of an automation.
type Run struct {
	ID           string          `json:"id"`
	Automation   string          `json:"automation"`
	Workspace    string          `json:"workspace"`
	ManifestHash string          `json:"manifestHash"`
	TriggerType  string          `json:"triggerType"`
	TriggerKey   string          `json:"triggerKey,omitempty"`
	ScheduledFor *time.Time      `json:"scheduledFor,omitempty"`
	TriggerData  json.RawMessage `json:"triggerData"`
	Status       string          `json:"status"`
	Error        string          `json:"error,omitempty"`
	Anomalies    int             `json:"anomalies"`
	QueuedAt     time.Time       `json:"queuedAt"`
	StartedAt    *time.Time      `json:"startedAt,omitempty"`
	FinishedAt   *time.Time      `json:"finishedAt,omitempty"`
	CostEur      float64         `json:"costEur"`
	Worker       string          `json:"worker,omitempty"`
}

// NewRun describes a run to enqueue.
type NewRun struct {
	Automation string
	// Workspace defaults to "default".
	Workspace    string
	ManifestHash string
	TriggerType  string
	TriggerKey   string
	ScheduledFor *time.Time
	TriggerData  any
}

const runColumns = `id, automation, workspace, manifest_hash, trigger_type, trigger_key, scheduled_for, trigger_data,
	status, error, anomalies, queued_at, started_at, finished_at, cost_eur, worker`

func scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var (
		r                          Run
		scheduled, started, finish sql.NullString
		queued, data               string
	)
	err := row.Scan(&r.ID, &r.Automation, &r.Workspace, &r.ManifestHash, &r.TriggerType, &r.TriggerKey, &scheduled, &data,
		&r.Status, &r.Error, &r.Anomalies, &queued, &started, &finish, &r.CostEur, &r.Worker)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ScheduledFor = parseTS(scheduled)
	r.StartedAt = parseTS(started)
	r.FinishedAt = parseTS(finish)
	r.QueuedAt = mustTS(queued)
	r.TriggerData = json.RawMessage(data)
	return &r, nil
}

// Enqueue adds a queued run. For scheduled runs it is idempotent: the same
// occurrence is enqueued once, and inserted reports whether this call did it.
func (s *Store) Enqueue(ctx context.Context, n NewRun, now time.Time) (id string, inserted bool, err error) {
	id = NewID(now)
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO runs
		(id, automation, workspace, manifest_hash, trigger_type, trigger_key, scheduled_for, trigger_data, status, queued_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.Automation, workspaceOr(n.Workspace), n.ManifestHash, n.TriggerType, n.TriggerKey, tsPtr(n.ScheduledFor), toJSON(n.TriggerData), RunQueued, ts(now))
	if err != nil {
		return "", false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", false, err
	}
	return id, affected == 1, nil
}

// StartNow creates a run that is already running, for runs started from
// the command line outside the queue.
func (s *Store) StartNow(ctx context.Context, n NewRun, worker string, now time.Time) (*Run, error) {
	id := NewID(now)
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs
		(id, automation, workspace, manifest_hash, trigger_type, trigger_key, scheduled_for, trigger_data, status, queued_at, started_at, worker)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.Automation, workspaceOr(n.Workspace), n.ManifestHash, n.TriggerType, n.TriggerKey, tsPtr(n.ScheduledFor), toJSON(n.TriggerData),
		RunRunning, ts(now), ts(now), worker)
	if err != nil {
		return nil, err
	}
	return s.GetRun(ctx, id)
}

// Claim takes the oldest queued run whose automation has no run in
// progress, marks it running for worker and returns it. It returns nil when
// nothing is ready.
func (s *Store) Claim(ctx context.Context, worker string, lease time.Duration, now time.Time) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE runs
		SET status = ?, started_at = ?, worker = ?, lease_until = ?
		WHERE id = (
			SELECT q.id FROM runs q
			WHERE q.status = ?
			  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.automation = q.automation AND r.status = ?)
			ORDER BY q.queued_at, q.id
			LIMIT 1)
		RETURNING `+runColumns,
		RunRunning, ts(now), worker, ts(now.Add(lease)), RunQueued, RunRunning)
	r, err := scanRun(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// RenewLease extends the lease of a running run held by worker.
func (s *Store) RenewLease(ctx context.Context, id, worker string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET lease_until = ? WHERE id = ? AND worker = ? AND status = ?`,
		ts(until), id, worker, RunRunning)
	return err
}

// Finish records the outcome of a run.
func (s *Store) Finish(ctx context.Context, id, status, errMsg string, anomalies int, cost float64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ?, error = ?, anomalies = ?, cost_eur = ?, finished_at = ?, lease_until = NULL
		WHERE id = ?`, status, errMsg, anomalies, cost, ts(now), id)
	return err
}

// RecoverInterrupted marks runs whose lease expired as failed. It runs at
// start-up and periodically, so a crash never leaves a run "running".
func (s *Store) RecoverInterrupted(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ?, finished_at = ?, lease_until = NULL,
		error = 'interrupted: Pendolo stopped while this run was in progress'
		WHERE status = ? AND (lease_until IS NULL OR lease_until < ?)`,
		RunFailed, ts(now), RunRunning, ts(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetRun returns one run.
func (s *Store) GetRun(ctx context.Context, id string) (*Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
}

// RunFilter selects runs to list.
type RunFilter struct {
	Workspace  string
	Automation string
	Status     string
	// Before returns the runs listed after this run id (the next page).
	Before string
	Limit  int
}

func workspaceOr(ws string) string {
	if ws == "" {
		return DefaultWorkspace
	}
	return ws
}

// ListRuns returns runs, newest first.
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM runs
		WHERE (? = '' OR workspace = ?) AND (? = '' OR automation = ?) AND (? = '' OR status = ?)
		  AND (? = '' OR (queued_at, id) < (SELECT queued_at, id FROM runs WHERE id = ?))
		ORDER BY queued_at DESC, id DESC LIMIT ?`,
		f.Workspace, f.Workspace, f.Automation, f.Automation, f.Status, f.Status, f.Before, f.Before, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountQueued returns how many runs wait in the queue.
func (s *Store) CountQueued(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status = ?`, RunQueued).Scan(&n)
	return n, err
}

// DeleteRunsBefore removes finished runs older than cutoff, with their steps
// and model calls.
func (s *Store) DeleteRunsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runs WHERE finished_at IS NOT NULL AND finished_at < ?`, ts(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
