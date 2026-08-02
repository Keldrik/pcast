package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Keldrik/pcast/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrate(ctx context.Context, db *sql.DB) error {
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
	if len(migs) == 0 {
		return model.Storage("no migrations found", nil)
	}

	// BEGIN IMMEDIATE makes schema creation/upgrades serialize across separate
	// processes. A normal database/sql transaction starts deferred and allows
	// two first opens to race before either one writes user_version.
	conn, err := db.Conn(ctx)
	if err != nil {
		return model.Storage("acquire migration connection", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return model.Storage("begin migration", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	var ver int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&ver); err != nil {
		return model.Storage("read schema version", err)
	}
	latest := migs[len(migs)-1].version
	if ver > latest {
		return model.Storage(fmt.Sprintf("database schema version %d is newer than supported version %d", ver, latest), nil)
	}

	for _, m := range migs {
		if m.version <= ver {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return model.Storage("read migration "+m.name, err)
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return model.Storage("apply migration "+m.name, err)
		}
		if m.version == 2 {
			if err := backfillPublishedAtNanos(ctx, conn); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			return model.Storage("set schema version", err)
		}
		ver = m.version
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return model.Storage("commit migrations", err)
	}
	committed = true
	return nil
}

func backfillPublishedAtNanos(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, published_at FROM episodes
		WHERE published_at IS NOT NULL AND published_at_ns IS NULL`)
	if err != nil {
		return model.Storage("read publication timestamps", err)
	}
	type publishedAt struct {
		id int64
		t  time.Time
	}
	var values []publishedAt
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return model.Storage("scan publication timestamp", err)
		}
		t, err := parseTime(raw)
		if err != nil {
			_ = rows.Close()
			return model.Storage("parse publication timestamp", err)
		}
		values = append(values, publishedAt{id: id, t: t})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return model.Storage("read publication timestamps", err)
	}
	if err := rows.Close(); err != nil {
		return model.Storage("close publication timestamps", err)
	}
	for _, value := range values {
		if _, err := conn.ExecContext(ctx,
			`UPDATE episodes SET published_at_ns = ? WHERE id = ?`,
			value.t.UnixNano(), value.id); err != nil {
			return model.Storage("backfill publication timestamp", err)
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
