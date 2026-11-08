package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Connection is a link to an outside service in a workspace.
type Connection struct {
	Workspace string         `json:"workspace"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Config    map[string]any `json:"config"`
	// Sealed holds the credentials, encrypted; callers open it with the vault.
	Sealed    string    `json:"-"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Account   string    `json:"account,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy"`
}

// Connection statuses.
const (
	ConnReady     = "ready"
	ConnNeedsAuth = "needs_auth"
	ConnError     = "error"
)

// PutConnection creates or replaces a connection.
func (s *Store) PutConnection(ctx context.Context, c Connection) error {
	if c.Config == nil {
		c.Config = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO connections (workspace_id, name, kind, config, sealed, status, error, account, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, name) DO UPDATE SET kind = excluded.kind, config = excluded.config, sealed = excluded.sealed,
			status = excluded.status, error = excluded.error, account = excluded.account, updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		c.Workspace, c.Name, c.Kind, toJSON(c.Config), c.Sealed, c.Status, c.Error, c.Account, ts(c.UpdatedAt), c.UpdatedBy)
	return err
}

// UpdateConnectionState records status, error, account and refreshed
// credentials without touching the rest.
func (s *Store) UpdateConnectionState(ctx context.Context, workspace, name, sealed, status, errMsg, account string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE connections SET sealed = ?, status = ?, error = ?, account = CASE WHEN ? = '' THEN account ELSE ? END,
		updated_at = ? WHERE workspace_id = ? AND name = ?`, sealed, status, errMsg, account, account, ts(now), workspace, name)
	return err
}

const connColumns = `workspace_id, name, kind, config, sealed, status, error, account, updated_at, updated_by`

func scanConnection(row interface{ Scan(...any) error }) (*Connection, error) {
	var (
		c       Connection
		cfg, at string
	)
	err := row.Scan(&c.Workspace, &c.Name, &c.Kind, &cfg, &c.Sealed, &c.Status, &c.Error, &c.Account, &at, &c.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(cfg), &c.Config)
	if c.Config == nil {
		c.Config = map[string]any{}
	}
	c.UpdatedAt = mustTS(at)
	return &c, nil
}

// GetConnection returns one connection.
func (s *Store) GetConnection(ctx context.Context, workspace, name string) (*Connection, error) {
	return scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connColumns+` FROM connections WHERE workspace_id = ? AND name = ?`, workspace, name))
}

// ListConnections returns the connections of a workspace.
func (s *Store) ListConnections(ctx context.Context, workspace string) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connColumns+` FROM connections WHERE workspace_id = ? ORDER BY name`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// DeleteConnection removes a connection.
func (s *Store) DeleteConnection(ctx context.Context, workspace, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE workspace_id = ? AND name = ?`, workspace, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// WorkspaceAutonomy returns a workspace's maximum autonomy ("auto" if unknown).
func (s *Store) WorkspaceAutonomy(ctx context.Context, workspace string) (string, error) {
	var a string
	err := s.db.QueryRowContext(ctx, `SELECT max_autonomy FROM workspaces WHERE id = ?`, workspace).Scan(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return "auto", nil
	}
	return a, err
}

// WorkspaceSettings are editable workspace properties.
type WorkspaceSettings struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MaxAutonomy string `json:"maxAutonomy"`
}

// GetWorkspaceSettings returns a workspace's settings.
func (s *Store) GetWorkspaceSettings(ctx context.Context, workspace string) (*WorkspaceSettings, error) {
	var w WorkspaceSettings
	err := s.db.QueryRowContext(ctx, `SELECT id, name, max_autonomy FROM workspaces WHERE id = ?`, workspace).Scan(&w.ID, &w.Name, &w.MaxAutonomy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &w, err
}

// SetWorkspaceSettings stores a workspace's name and maximum autonomy.
func (s *Store) SetWorkspaceSettings(ctx context.Context, w WorkspaceSettings) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workspaces SET name = ?, max_autonomy = ? WHERE id = ?`, w.Name, w.MaxAutonomy, w.ID)
	return err
}

// MarkEmailSeen records message ids for an email trigger and returns the
// ones that were new.
func (s *Store) MarkEmailSeen(ctx context.Context, automation, key string, ids []string, now time.Time) ([]string, error) {
	var fresh []string
	for _, id := range ids {
		res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO email_seen (automation, trigger_key, message_id, seen_at) VALUES (?, ?, ?, ?)`,
			automation, key, id, ts(now))
		if err != nil {
			return fresh, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			fresh = append(fresh, id)
		}
	}
	return fresh, nil
}

// PruneEmailSeen forgets message ids older than cutoff.
func (s *Store) PruneEmailSeen(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM email_seen WHERE seen_at < ?`, ts(cutoff))
	return err
}
