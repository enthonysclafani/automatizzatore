// Package store keeps Pendolo's state in SQLite: the run queue, schedule
// state, and the full history of runs, steps and model calls.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Run statuses.
const (
	RunQueued    = "queued"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Step statuses.
const (
	StepRunning   = "running"
	StepSucceeded = "succeeded"
	StepFailed    = "failed"
	StepSkipped   = "skipped"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store is a handle on the database. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path and migrates it.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; one connection avoids lock errors
	// and is plenty for this workload.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, ts(time.Now())); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// timeLayout is fixed-width UTC, so text order is time order.
const timeLayout = "2006-01-02T15:04:05.000000Z"

func ts(t time.Time) string { return t.UTC().Format(timeLayout) }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func parseTS(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(timeLayout, s.String)
	if err != nil {
		return nil
	}
	return &t
}

func mustTS(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

// NewID returns a sortable unique id: 10 characters of millisecond time,
// then 16 random characters (Crockford base32).
func NewID(now time.Time) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	var b [26]byte
	ms := uint64(now.UnixMilli())
	for i := 9; i >= 0; i-- {
		b[i] = alphabet[ms&31]
		ms >>= 5
	}
	var r [16]byte
	_, _ = rand.Read(r[:])
	for i := 0; i < 16; i++ {
		b[10+i] = alphabet[r[i]&31]
	}
	return string(b[:])
}

func toJSON(v any) string {
	if v == nil {
		return "null"
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return "null"
		}
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(fmt.Sprintf("<unencodable: %v>", err))
	}
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
