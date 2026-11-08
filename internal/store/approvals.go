package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// RunWaiting is the status of a run paused for an approval.
const RunWaiting = "waiting"

// StepWaiting is the status of the step a run is paused at.
const StepWaiting = "waiting"

// Approval statuses.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalExpired  = "expired"
)

// Approval is a decision a person must take before a run continues.
type Approval struct {
	ID          string     `json:"id"`
	RunID       string     `json:"runId"`
	Workspace   string     `json:"workspace"`
	Automation  string     `json:"automation"`
	StepPath    string     `json:"stepPath"`
	Message     string     `json:"message"`
	Detail      any        `json:"detail"`
	Approvers   []string   `json:"approvers"`
	Status      string     `json:"status"`
	OnTimeout   string     `json:"onTimeout"`
	RequestedAt time.Time  `json:"requestedAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
	DecidedBy   string     `json:"decidedBy,omitempty"`
	Comment     string     `json:"comment,omitempty"`
}

const approvalColumns = `id, run_id, workspace, automation, step_path, message, detail, approvers, status, on_timeout,
	requested_at, expires_at, decided_at, decided_by, comment`

func scanApproval(row interface{ Scan(...any) error }) (*Approval, error) {
	var (
		a                          Approval
		detail, approvers, req, ex string
		decided                    sql.NullString
	)
	err := row.Scan(&a.ID, &a.RunID, &a.Workspace, &a.Automation, &a.StepPath, &a.Message, &detail, &approvers, &a.Status,
		&a.OnTimeout, &req, &ex, &decided, &a.DecidedBy, &a.Comment)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(detail), &a.Detail)
	_ = json.Unmarshal([]byte(approvers), &a.Approvers)
	a.RequestedAt, a.ExpiresAt, a.DecidedAt = mustTS(req), mustTS(ex), parseTS(decided)
	return &a, nil
}

// Pause records a pending approval, stores the run's checkpoint and marks
// the run waiting, in one transaction.
func (s *Store) Pause(ctx context.Context, a Approval, checkpoint string, cost float64, anomalies int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO approvals (`+approvalColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, '', '')`,
		a.ID, a.RunID, a.Workspace, a.Automation, a.StepPath, a.Message, toJSON(a.Detail), toJSON(a.Approvers), ApprovalPending,
		a.OnTimeout, ts(a.RequestedAt), ts(a.ExpiresAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, checkpoint = ?, cost_eur = ?, anomalies = ?, lease_until = NULL, worker = ''
		WHERE id = ?`, RunWaiting, checkpoint, cost, anomalies, a.RunID); err != nil {
		return err
	}
	return tx.Commit()
}

// Decide records a decision on a pending approval. Approved runs go back
// to the queue to resume; rejected or expired runs are cancelled. It
// returns ErrConflict when the approval is no longer pending.
func (s *Store) Decide(ctx context.Context, id, status, by, comment string, now time.Time) (*Approval, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanApproval(tx.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if a.Status != ApprovalPending {
		return a, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_at = ?, decided_by = ?, comment = ? WHERE id = ?`,
		status, ts(now), by, comment, id); err != nil {
		return nil, err
	}
	if status == ApprovalApproved {
		_, err = tx.ExecContext(ctx, `UPDATE runs SET status = ?, queued_at = ? WHERE id = ? AND status = ?`, RunQueued, ts(now), a.RunID, RunWaiting)
		if err == nil {
			// The waiting record becomes the decision; a gated action then runs as a new record.
			_, err = tx.ExecContext(ctx, `UPDATE steps SET status = ?, output = ?, finished_at = ? WHERE run_id = ? AND status = ?`,
				StepSucceeded, toJSON(map[string]any{"approved": true, "by": by, "comment": comment}), ts(now), a.RunID, StepWaiting)
		}
	} else {
		reason := "rejected by " + by
		if status == ApprovalExpired {
			reason = "approval expired"
		}
		if comment != "" {
			reason += ": " + comment
		}
		_, err = tx.ExecContext(ctx, `UPDATE runs SET status = ?, error = ?, finished_at = ?, checkpoint = '' WHERE id = ? AND status = ?`,
			RunCancelled, reason, ts(now), a.RunID, RunWaiting)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE steps SET status = ?, error = ?, finished_at = ? WHERE run_id = ? AND status = ?`,
				StepFailed, reason, ts(now), a.RunID, StepWaiting)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.Status, a.DecidedBy, a.Comment, a.DecidedAt = status, by, comment, &now
	return a, nil
}

// GetApproval returns one approval.
func (s *Store) GetApproval(ctx context.Context, id string) (*Approval, error) {
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id))
}

// ApprovalFilter selects approvals.
type ApprovalFilter struct {
	Workspace string
	RunID     string
	Status    string
	Limit     int
}

// ListApprovals returns approvals, newest first (oldest first for one run).
func (s *Store) ListApprovals(ctx context.Context, f ApprovalFilter) ([]Approval, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	order := "DESC"
	if f.RunID != "" {
		order = "ASC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+approvalColumns+` FROM approvals
		WHERE (? = '' OR workspace = ?) AND (? = '' OR run_id = ?) AND (? = '' OR status = ?)
		ORDER BY requested_at `+order+` LIMIT ?`,
		f.Workspace, f.Workspace, f.RunID, f.RunID, f.Status, f.Status, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ExpiredApprovals returns pending approvals past their expiry.
func (s *Store) ExpiredApprovals(ctx context.Context, now time.Time) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE status = ? AND expires_at < ?`, ApprovalPending, ts(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// CountPendingApprovals counts pending approvals in a workspace.
func (s *Store) CountPendingApprovals(ctx context.Context, workspace string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE workspace = ? AND status = ?`, workspace, ApprovalPending).Scan(&n)
	return n, err
}

// Checkpoint returns a run's saved progress ("" when none).
func (s *Store) Checkpoint(ctx context.Context, runID string) (string, error) {
	var c string
	err := s.db.QueryRowContext(ctx, `SELECT checkpoint FROM runs WHERE id = ?`, runID).Scan(&c)
	return c, err
}

// MaxStepSeq returns the highest step number recorded for a run.
func (s *Store) MaxStepSeq(ctx context.Context, runID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM steps WHERE run_id = ?`, runID).Scan(&n)
	return n, err
}

// MarkStepWaiting records that a step waits for an approval.
func (s *Store) MarkStepWaiting(ctx context.Context, runID string, seq int, input any, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE steps SET status = ?, input = ?, finished_at = ? WHERE run_id = ? AND seq = ?`,
		StepWaiting, toJSON(input), ts(now), runID, seq)
	return err
}
