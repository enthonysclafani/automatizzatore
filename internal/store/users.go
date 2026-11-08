package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DefaultWorkspace holds automations that name no workspace.
const DefaultWorkspace = "default"

// Workspace roles, from the least to the most powerful.
const (
	RoleViewer   = "viewer"
	RoleApprover = "approver"
	RoleBuilder  = "builder"
	RoleAdmin    = "admin"
)

// ValidRole reports whether r is a workspace role.
func ValidRole(r string) bool {
	switch r {
	case RoleViewer, RoleApprover, RoleBuilder, RoleAdmin:
		return true
	}
	return false
}

// ErrConflict is returned when a unique value is already taken.
var ErrConflict = errors.New("already exists")

// Workspace groups automations, runs, secrets and members.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// EnsureWorkspace creates a workspace named after its id if it is missing.
func (s *Store) EnsureWorkspace(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (id, name, created_at) VALUES (?, ?, ?)`, id, id, ts(now))
	return err
}

// CreateWorkspace adds a workspace; ErrConflict if the id is taken.
func (s *Store) CreateWorkspace(ctx context.Context, w Workspace) error {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO workspaces (id, name, created_at) VALUES (?, ?, ?)`, w.ID, w.Name, ts(w.CreatedAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// ListWorkspaces returns every workspace by id.
func (s *Store) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM workspaces ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		var at string
		if err := rows.Scan(&w.ID, &w.Name, &at); err != nil {
			return nil, err
		}
		w.CreatedAt = mustTS(at)
		out = append(out, w)
	}
	return out, rows.Err()
}

// User is a person who signs in to the dashboard.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	IsOwner      bool       `json:"isOwner"`
	Disabled     bool       `json:"disabled"`
	TOTPEnabled  bool       `json:"totpEnabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
	PasswordHash string     `json:"-"`
	TOTPSecret   string     `json:"-"` // sealed
	TOTPLast     int64      `json:"-"`
}

const userColumns = `id, email, name, password_hash, totp_secret, totp_enabled, totp_last, is_owner, disabled, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u                              User
		totpEnabled, isOwner, disabled int
		created                        string
		lastLogin                      sql.NullString
	)
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.TOTPSecret, &totpEnabled, &u.TOTPLast, &isOwner, &disabled, &created, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TOTPEnabled, u.IsOwner, u.Disabled = totpEnabled == 1, isOwner == 1, disabled == 1
	u.CreatedAt = mustTS(created)
	u.LastLoginAt = parseTS(lastLogin)
	return &u, nil
}

// CountUsers returns how many users exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser adds a user; ErrConflict if the email is taken.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	if u.ID == "" {
		u.ID = NewID(u.CreatedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, email, name, password_hash, is_owner, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.PasswordHash, boolInt(u.IsOwner), ts(u.CreatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrConflict
	}
	return err
}

// GetUser returns a user by id.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUserByEmail returns a user by email, ignoring case.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, strings.TrimSpace(email)))
}

// ListUsers returns every user by email.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUser stores name, owner and disabled flags, password and TOTP state.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET name = ?, password_hash = ?, totp_secret = ?, totp_enabled = ?, totp_last = ?,
		is_owner = ?, disabled = ? WHERE id = ?`,
		u.Name, u.PasswordHash, u.TOTPSecret, boolInt(u.TOTPEnabled), u.TOTPLast, boolInt(u.IsOwner), boolInt(u.Disabled), u.ID)
	return err
}

// TouchLogin records a successful sign-in.
func (s *Store) TouchLogin(ctx context.Context, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, ts(now), userID)
	return err
}

// SetTOTPLast stores the last accepted TOTP time step.
func (s *Store) SetTOTPLast(ctx context.Context, userID string, step int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_last = ? WHERE id = ?`, step, userID)
	return err
}

// CountOwners returns how many enabled owners exist.
func (s *Store) CountOwners(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_owner = 1 AND disabled = 0`).Scan(&n)
	return n, err
}

// Membership is a user's role in a workspace.
type Membership struct {
	UserID      string `json:"userId"`
	WorkspaceID string `json:"workspaceId"`
	Role        string `json:"role"`
	Email       string `json:"email"`
	Name        string `json:"name"`
}

// SetMembership adds a user to a workspace or changes their role.
func (s *Store) SetMembership(ctx context.Context, userID, workspaceID, role string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO memberships (user_id, workspace_id, role) VALUES (?, ?, ?)
		ON CONFLICT (user_id, workspace_id) DO UPDATE SET role = excluded.role`, userID, workspaceID, role)
	return err
}

// RemoveMembership removes a user from a workspace.
func (s *Store) RemoveMembership(ctx context.Context, userID, workspaceID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memberships WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Role returns a user's role in a workspace, or "" when not a member.
func (s *Store) Role(ctx context.Context, userID, workspaceID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM memberships WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// UserMemberships returns the workspaces a user belongs to.
func (s *Store) UserMemberships(ctx context.Context, userID string) ([]Membership, error) {
	return s.memberships(ctx, userID, "")
}

// WorkspaceMembers returns the members of a workspace.
func (s *Store) WorkspaceMembers(ctx context.Context, workspaceID string) ([]Membership, error) {
	return s.memberships(ctx, "", workspaceID)
}

func (s *Store) memberships(ctx context.Context, userID, workspaceID string) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.user_id, m.workspace_id, m.role, u.email, u.name
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE (? = '' OR m.user_id = ?) AND (? = '' OR m.workspace_id = ?)
		ORDER BY m.workspace_id, u.email`, userID, userID, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.UserID, &m.WorkspaceID, &m.Role, &m.Email, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Session is a signed-in browser.
type Session struct {
	UserID    string
	ExpiresAt time.Time
	LastSeen  time.Time
}

// CreateSession stores a session by the hash of its token.
func (s *Store) CreateSession(ctx context.Context, tokenHash, userID, ip, userAgent string, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, tokenHash, userID, ts(now), ts(expires), ts(now), ip, truncate(userAgent, 300))
	return err
}

// GetSession returns a session that has not expired.
func (s *Store) GetSession(ctx context.Context, tokenHash string, now time.Time) (*Session, error) {
	var userID, expires, seen string
	err := s.db.QueryRowContext(ctx, `SELECT user_id, expires_at, last_seen_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, ts(now)).Scan(&userID, &expires, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Session{UserID: userID, ExpiresAt: mustTS(expires), LastSeen: mustTS(seen)}, nil
}

// ExtendSession slides a session's expiry.
func (s *Store) ExtendSession(ctx context.Context, tokenHash string, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`, ts(now), ts(expires), tokenHash)
	return err
}

// DeleteSession signs a browser out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions signs a user out everywhere, except keep (may be "").
func (s *Store) DeleteUserSessions(ctx context.Context, userID, keep string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, keep)
	return err
}

// DeleteExpiredSessions removes sessions past their expiry.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ts(now))
	return err
}

// APIToken is a credential for scripts.
type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// CreateAPIToken stores a token by its hash.
func (s *Store) CreateAPIToken(ctx context.Context, t APIToken, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, tokenHash, ts(t.CreatedAt), tsPtr(t.ExpiresAt))
	return err
}

// GetAPITokenByHash returns a token that has not expired.
func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string, now time.Time) (*APIToken, error) {
	var (
		t             APIToken
		created       string
		expires, used sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, name, created_at, expires_at, last_used_at FROM api_tokens
		WHERE token_hash = ? AND (expires_at IS NULL OR expires_at > ?)`, tokenHash, ts(now)).
		Scan(&t.ID, &t.UserID, &t.Name, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt, t.ExpiresAt, t.LastUsedAt = mustTS(created), parseTS(expires), parseTS(used)
	return &t, nil
}

// TouchAPIToken records that a token was used.
func (s *Store) TouchAPIToken(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, ts(now), id)
	return err
}

// ListAPITokens returns a user's tokens.
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, created_at, expires_at, last_used_at FROM api_tokens
		WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var (
			t             APIToken
			created       string
			expires, used sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &created, &expires, &used); err != nil {
			return nil, err
		}
		t.CreatedAt, t.ExpiresAt, t.LastUsedAt = mustTS(created), parseTS(expires), parseTS(used)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of a user's tokens.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
