package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Draft statuses.
const (
	DraftOpen      = "open"
	DraftProposed  = "proposed"
	DraftActivated = "activated"
	DraftDiscarded = "discarded"
)

// Draft is an automation being written with the AI builder.
type Draft struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	// Automation is metadata.name of the draft, '' until the model writes one.
	Automation string `json:"automation"`
	// BaseName and BaseHash name the version the draft edits; empty for a new automation.
	BaseName string            `json:"baseName,omitempty"`
	BaseHash string            `json:"baseHash,omitempty"`
	Files    map[string]string `json:"files"`
	// Problems is the latest validation, as raw JSON owned by the caller.
	Problems   json.RawMessage `json:"problems"`
	Hash       string          `json:"hash"`
	Status     string          `json:"status"`
	DryRunID   string          `json:"dryRunId,omitempty"`
	DryRunHash string          `json:"dryRunHash,omitempty"`
	CostEur    float64         `json:"costEur"`
	CreatedBy  string          `json:"createdBy"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
	ProposedBy string          `json:"proposedBy,omitempty"`
	ProposedAt *time.Time      `json:"proposedAt,omitempty"`
	DecidedBy  string          `json:"decidedBy,omitempty"`
	DecidedAt  *time.Time      `json:"decidedAt,omitempty"`
	Comment    string          `json:"comment,omitempty"`
}

// DraftMessage is one turn of a builder conversation.
type DraftMessage struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Author    string    `json:"author,omitempty"`
	Text      string    `json:"text"`
	Hash      string    `json:"hash,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	// RoleEvent marks things that happened to the draft, such as a manual edit.
	RoleEvent = "event"
)

const draftColumns = `id, workspace, automation, base_name, base_hash, files, problems, hash, status, dry_run_id, dry_run_hash,
	cost_eur, created_by, created_at, updated_at, proposed_by, proposed_at, decided_by, decided_at, comment`

func scanDraft(row interface{ Scan(...any) error }) (*Draft, error) {
	var (
		d                 Draft
		files, problems   string
		created, updated  string
		proposed, decided sql.NullString
	)
	err := row.Scan(&d.ID, &d.Workspace, &d.Automation, &d.BaseName, &d.BaseHash, &files, &problems, &d.Hash, &d.Status,
		&d.DryRunID, &d.DryRunHash, &d.CostEur, &d.CreatedBy, &created, &updated, &d.ProposedBy, &proposed, &d.DecidedBy, &decided, &d.Comment)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(files), &d.Files)
	if d.Files == nil {
		d.Files = map[string]string{}
	}
	d.Problems = json.RawMessage(problems)
	d.CreatedAt, d.UpdatedAt = mustTS(created), mustTS(updated)
	d.ProposedAt, d.DecidedAt = parseTS(proposed), parseTS(decided)
	return &d, nil
}

// CreateDraft inserts a draft; ID, Status and the times are filled in.
func (s *Store) CreateDraft(ctx context.Context, d *Draft, now time.Time) error {
	d.ID = NewID(now)
	d.Status = DraftOpen
	d.CreatedAt, d.UpdatedAt = now.UTC(), now.UTC()
	if d.Files == nil {
		d.Files = map[string]string{}
	}
	if len(d.Problems) == 0 {
		d.Problems = json.RawMessage("[]")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO drafts (id, workspace, automation, base_name, base_hash, files, problems, hash, status,
		cost_eur, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Workspace, d.Automation, d.BaseName, d.BaseHash, toJSON(d.Files), string(d.Problems), d.Hash, d.Status,
		d.CostEur, d.CreatedBy, ts(now), ts(now))
	return err
}

// GetDraft returns one draft.
func (s *Store) GetDraft(ctx context.Context, id string) (*Draft, error) {
	return scanDraft(s.db.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ?`, id))
}

// ListDrafts returns the drafts of a workspace, newest first. An empty
// status lists the open and proposed ones.
func (s *Store) ListDrafts(ctx context.Context, workspace, status string, limit int) ([]Draft, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := `SELECT ` + draftColumns + ` FROM drafts WHERE workspace = ?`
	args := []any{workspace}
	if status == "" {
		q += ` AND status IN (?, ?)`
		args = append(args, DraftOpen, DraftProposed)
	} else if status != "all" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Draft{}
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// CountProposedDrafts counts drafts waiting for activation in a workspace.
func (s *Store) CountProposedDrafts(ctx context.Context, workspace string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drafts WHERE workspace = ? AND status = ?`, workspace, DraftProposed).Scan(&n)
	return n, err
}

// SaveDraftContent stores new files, validation and name. A proposed draft
// goes back to open: what was proposed is no longer what it holds.
func (s *Store) SaveDraftContent(ctx context.Context, d *Draft, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE drafts SET automation = ?, files = ?, problems = ?, hash = ?, updated_at = ?,
		status = CASE WHEN status = ? THEN ? ELSE status END, proposed_by = CASE WHEN status = ? THEN '' ELSE proposed_by END,
		proposed_at = CASE WHEN status = ? THEN NULL ELSE proposed_at END
		WHERE id = ? AND status IN (?, ?)`,
		d.Automation, toJSON(d.Files), string(d.Problems), d.Hash, ts(now),
		DraftProposed, DraftOpen, DraftProposed, DraftProposed, d.ID, DraftOpen, DraftProposed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// AddDraftCost adds model spending to a draft.
func (s *Store) AddDraftCost(ctx context.Context, id string, cost float64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drafts SET cost_eur = cost_eur + ? WHERE id = ?`, cost, id)
	return err
}

// SetDraftDryRun links the latest dry run of a draft.
func (s *Store) SetDraftDryRun(ctx context.Context, id, runID, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drafts SET dry_run_id = ?, dry_run_hash = ?, updated_at = ? WHERE id = ?`, runID, hash, ts(now), id)
	return err
}

// SetDraftStatus moves a draft from one of the from statuses to status.
// It returns ErrConflict when the draft is in none of them.
func (s *Store) SetDraftStatus(ctx context.Context, id, status, by, comment string, from []string, now time.Time) error {
	q := `UPDATE drafts SET status = ?, updated_at = ?`
	args := []any{status, ts(now)}
	switch status {
	case DraftProposed:
		q += `, proposed_by = ?, proposed_at = ?`
		args = append(args, by, ts(now))
	default:
		q += `, decided_by = ?, decided_at = ?, comment = ?`
		args = append(args, by, ts(now), comment)
	}
	q += ` WHERE id = ? AND status IN (`
	args = append(args, id)
	for i, f := range from {
		if i > 0 {
			q += `, `
		}
		q += `?`
		args = append(args, f)
	}
	q += `)`
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// AddDraftMessage appends a message to a draft's conversation.
func (s *Store) AddDraftMessage(ctx context.Context, draftID string, m DraftMessage, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO draft_messages (draft_id, role, author, text, hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		draftID, m.Role, m.Author, m.Text, m.Hash, ts(now))
	return err
}

// ListDraftMessages returns a draft's conversation, oldest first.
func (s *Store) ListDraftMessages(ctx context.Context, draftID string) ([]DraftMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, role, author, text, hash, created_at FROM draft_messages WHERE draft_id = ? ORDER BY id`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DraftMessage{}
	for rows.Next() {
		var m DraftMessage
		var at string
		if err := rows.Scan(&m.ID, &m.Role, &m.Author, &m.Text, &m.Hash, &at); err != nil {
			return nil, err
		}
		m.CreatedAt = mustTS(at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// BuilderCall records a model call made by the builder.
type BuilderCall struct {
	DraftID      string
	Workspace    string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CostEur      float64
	PriceKnown   bool
	LatencyMs    int64
	Error        string
}

// AddBuilderCall records a builder model call.
func (s *Store) AddBuilderCall(ctx context.Context, c BuilderCall, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO builder_calls (draft_id, workspace, provider, model, input_tokens, output_tokens,
		cost_eur, price_known, latency_ms, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.DraftID, c.Workspace, c.Provider, c.Model, c.InputTokens, c.OutputTokens, c.CostEur, boolInt(c.PriceKnown), c.LatencyMs, c.Error, ts(now))
	return err
}

// Version sources.
const (
	VersionBuilder = "builder"
	VersionFolder  = "folder"
)

// Version is one version of an automation.
type Version struct {
	Automation  string            `json:"automation"`
	Hash        string            `json:"hash"`
	Workspace   string            `json:"workspace"`
	Files       map[string]string `json:"files,omitempty"`
	Source      string            `json:"source"`
	DraftID     string            `json:"draftId,omitempty"`
	ActivatedBy string            `json:"activatedBy,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// AddVersion records a version unless it is already known. It reports
// whether it was new.
func (s *Store) AddVersion(ctx context.Context, v Version, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO automation_versions (automation, hash, workspace, files, source, draft_id, activated_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (automation, hash) DO NOTHING`,
		v.Automation, v.Hash, v.Workspace, toJSON(v.Files), v.Source, v.DraftID, v.ActivatedBy, ts(now))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// HasVersion reports whether a version is recorded.
func (s *Store) HasVersion(ctx context.Context, automation, hash string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM automation_versions WHERE automation = ? AND hash = ?`, automation, hash).Scan(&n)
	return n > 0, err
}

// ListVersions returns the versions of an automation, newest first, without files.
func (s *Store) ListVersions(ctx context.Context, automation string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT automation, hash, workspace, source, draft_id, activated_by, created_at
		FROM automation_versions WHERE automation = ? ORDER BY created_at DESC, hash LIMIT 200`, automation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var at string
		if err := rows.Scan(&v.Automation, &v.Hash, &v.Workspace, &v.Source, &v.DraftID, &v.ActivatedBy, &at); err != nil {
			return nil, err
		}
		v.CreatedAt = mustTS(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVersion returns one version with its files.
func (s *Store) GetVersion(ctx context.Context, automation, hash string) (*Version, error) {
	var v Version
	var files, at string
	err := s.db.QueryRowContext(ctx, `SELECT automation, hash, workspace, files, source, draft_id, activated_by, created_at
		FROM automation_versions WHERE automation = ? AND hash = ?`, automation, hash).
		Scan(&v.Automation, &v.Hash, &v.Workspace, &files, &v.Source, &v.DraftID, &v.ActivatedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(files), &v.Files)
	v.CreatedAt = mustTS(at)
	return &v, nil
}
