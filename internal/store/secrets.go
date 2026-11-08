package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SecretInfo describes a stored secret without its value.
type SecretInfo struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy"`
}

// PutSecret stores a sealed secret value.
func (s *Store) PutSecret(ctx context.Context, workspace, name, sealed, by string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets (workspace_id, name, sealed, updated_at, updated_by) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`, workspace, name, sealed, ts(now), by)
	return err
}

// GetSecret returns a sealed secret value, or ErrNotFound.
func (s *Store) GetSecret(ctx context.Context, workspace, name string) (string, error) {
	var sealed string
	err := s.db.QueryRowContext(ctx, `SELECT sealed FROM secrets WHERE workspace_id = ? AND name = ?`, workspace, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return sealed, err
}

// ListSecrets returns the secrets of a workspace, without values.
func (s *Store) ListSecrets(ctx context.Context, workspace string) ([]SecretInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, updated_at, updated_by FROM secrets WHERE workspace_id = ? ORDER BY name`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretInfo
	for rows.Next() {
		var si SecretInfo
		var at string
		if err := rows.Scan(&si.Name, &at, &si.UpdatedBy); err != nil {
			return nil, err
		}
		si.UpdatedAt = mustTS(at)
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteSecret removes a secret.
func (s *Store) DeleteSecret(ctx context.Context, workspace, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE workspace_id = ? AND name = ?`, workspace, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID        int64          `json:"id"`
	At        time.Time      `json:"at"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Workspace string         `json:"workspace,omitempty"`
	Target    string         `json:"target,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	IP        string         `json:"ip,omitempty"`
}

// Audit records an action.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (at, actor, action, workspace, target, detail, ip) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts(e.At), e.Actor, e.Action, e.Workspace, e.Target, toJSON(e.Detail), e.IP)
	return err
}

// ListAudit returns recent entries, newest first; workspace "" means all.
func (s *Store) ListAudit(ctx context.Context, workspace string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, actor, action, workspace, target, detail, ip FROM audit_log
		WHERE (? = '' OR workspace = ?) ORDER BY id DESC LIMIT ?`, workspace, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var (
			e          AuditEntry
			at, detail string
		)
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Workspace, &e.Target, &detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = mustTS(at)
		_ = jsonUnmarshal(detail, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
