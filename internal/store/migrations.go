package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Keldrik/pcast/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrate(ctx context.Context, db *sql.DB) error {
	var ver int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&ver); err != nil {
		return model.Storage("read schema version", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return model.Storage("read migrations", err)
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		numStr := strings.SplitN(e.Name(), "_", 2)[0]
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return model.Storage(fmt.Sprintf("invalid migration name %s", e.Name()), err)
		}
		migs = append(migs, mig{version: n, name: e.Name()})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		if m.version <= ver {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return model.Storage("read migration "+m.name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return model.Storage("begin migration", err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return model.Storage("apply migration "+m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			_ = tx.Rollback()
			return model.Storage("set schema version", err)
		}
		if err := tx.Commit(); err != nil {
			return model.Storage("commit migration "+m.name, err)
		}
	}
	return nil
}

// SchemaVersion returns the database user_version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var ver int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&ver); err != nil {
		return 0, model.Storage("read schema version", err)
	}
	return ver, nil
}
