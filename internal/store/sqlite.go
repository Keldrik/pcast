package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Keldrik/pcast/internal/model"
)

// Store is the SQLite-backed persistence adapter.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at path, configures SQLite, and migrates.
func Open(ctx context.Context, path string) (*Store, error) {
	// modernc driver; _pragma query params applied per-connection via DSN.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, model.Storage("open database", err)
	}
	db.SetMaxOpenConns(1) // SQLite write safety; WAL still allows concurrent readers via same conn pool carefully
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, model.Storage("ping database", err)
	}

	// Ensure foreign keys on this connection (DSN pragma is best-effort across drivers).
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, model.Storage("enable foreign keys", err)
	}

	s := &Store{db: db}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return model.Storage("close database", err)
	}
	return nil
}

// DB exposes the underlying *sql.DB for doctor checks.
func (s *Store) DB() *sql.DB {
	return s.db
}

// ForeignKeysEnabled reports whether PRAGMA foreign_keys is on.
func (s *Store) ForeignKeysEnabled(ctx context.Context) (bool, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&v); err != nil {
		return false, model.Storage("check foreign keys", err)
	}
	return v == 1, nil
}

// withTx runs fn inside a transaction.
func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Storage("begin transaction", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return model.Storage("commit transaction", err)
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func scanTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func scanNullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func scanNullInt(ns sql.NullInt64) *int {
	if !ns.Valid {
		return nil
	}
	v := int(ns.Int64)
	return &v
}

func scanNullInt64(ns sql.NullInt64) *int64 {
	if !ns.Valid {
		return nil
	}
	v := ns.Int64
	return &v
}

// clockNow is overridable in tests via Store methods receiving now from callers.
func fmtErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if se, ok := err.(*model.Error); ok {
		return se
	}
	msg := err.Error()
	// Surface unique constraint violations as storage errors with context.
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return model.Storage(op+": unique constraint", err)
	}
	return model.Storage(op, err)
}

// quoteIdent is unused; kept for clarity that SQL identifiers are never user-sourced.
var _ = fmt.Sprintf
