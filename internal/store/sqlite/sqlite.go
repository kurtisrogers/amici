// Package sqlite implements the storage ports declared in internal/domain on
// top of SQLite.
//
// SQLite is the right default for Amici. A network for friends and family is
// mostly reads of small graphs, a single binary with a single file is
// something a community can actually self-host, and the pure-Go driver means
// no cgo and no system libraries to chase. The ports in internal/domain exist
// so that a deployment which outgrows this can add a Postgres implementation
// without a single business rule moving.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the SQLite-backed implementation of domain.Store.
type Store struct {
	db *sql.DB
}

var _ domain.Store = (*Store)(nil)

// Open connects to the database at path, applies pragmas and runs migrations.
// A path of ":memory:" gives an isolated in-process database, which is what
// the test suite uses.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := path
	if path == ":memory:" {
		// The shared cache and a single connection keep an in-memory database
		// addressable from every statement in the process.
		dsn = "file::memory:?cache=shared"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %s: %w", path, err)
	}

	// SQLite writes are serialised, so a large connection pool buys nothing
	// and costs lock contention. Reads are fast enough that a handful of
	// connections saturates the disk.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
	}
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	pragmas := []string{
		// Write-ahead logging lets readers proceed during a write, which is
		// what makes a single-file database usable for a web application.
		"PRAGMA journal_mode = WAL",
		// NORMAL trades a fsync per commit for a fsync per checkpoint. With
		// WAL this is durable across process crashes, only losing the last
		// commits if the machine loses power.
		"PRAGMA synchronous = NORMAL",
		// Foreign keys are off by default in SQLite, which is a trap. The
		// schema leans on ON DELETE CASCADE for account deletion.
		"PRAGMA foreign_keys = ON",
		// Wait rather than returning SQLITE_BUSY under concurrent writes.
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for diagnostics. Nothing in the service layer should
// reach for this; it exists so the developer console can report health.
func (s *Store) DB() *sql.DB { return s.db }

// migrate applies any embedded migration files that have not run yet.
//
// The mechanism is deliberately plain: filenames sorted lexically, each
// applied once inside a transaction, with the applied name recorded. There is
// no down migration, because rolling a schema backwards on live member data
// is a decision that deserves a human writing a forward migration rather than
// a tool guessing.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		) STRICT`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema_migrations: %w", err)
	}

	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	for _, entry := range entries {
		name := strings.TrimPrefix(entry, "migrations/")
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile(entry)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, formatTime(time.Now().UTC()),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// AppliedMigrations lists the migrations that have run, newest last. The
// developer console shows this.
func (s *Store) AppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// timeLayout is RFC3339 with nanoseconds, which sorts lexically in the same
// order it sorts chronologically. That property is what lets the feed use a
// plain string comparison as its cursor.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		// Tolerate values written by hand or by an older layout.
		if t2, err2 := time.Parse(time.RFC3339Nano, s); err2 == nil {
			return t2.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func scanNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullID(id *domain.ID) any {
	if id == nil || *id == "" {
		return nil
	}
	return string(*id)
}

func scanNullID(ns sql.NullString) *domain.ID {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	id := domain.ID(ns.String)
	return &id
}

// translate converts driver errors into the domain vocabulary so that callers
// never have to know SQLite exists.
func translate(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE") {
		return fmt.Errorf("%w: %s already exists", domain.ErrConflict, what)
	}
	if strings.Contains(msg, "FOREIGN KEY constraint failed") {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	if strings.Contains(msg, "CHECK constraint failed") {
		return fmt.Errorf("%w: %s was rejected by the schema", domain.ErrValidation, what)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// placeholders builds "?, ?, ?" for an IN clause of n items.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// idArgs converts identifiers into query arguments.
func idArgs(ids []domain.ID) []any {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, string(id))
	}
	return args
}

// boolToInt keeps SQLite's integer booleans out of the calling code.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// withTx runs fn inside a transaction, rolling back on error.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
