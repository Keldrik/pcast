package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/platform"
)

// Doctor runs non-destructive diagnostics.
func (a *App) Doctor(ctx context.Context) (model.DoctorResult, error) {
	res := model.DoctorResult{DataDir: a.DataDir, Checks: []model.DoctorCheck{}, OK: true}
	dataDirOK := false

	if a.DataDir == "" {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "data_dir", Status: "error", Message: "data directory is not configured",
		})
		res.OK = false
	} else if err := platform.EnsureDataDir(a.DataDir); err != nil {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "data_dir", Status: "error", Message: err.Error(),
		})
		res.OK = false
	} else if _, err := os.ReadDir(a.DataDir); err != nil {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "data_dir", Status: "error", Message: "not readable: " + err.Error(),
		})
		res.OK = false
	} else {
		probe := filepath.Join(a.DataDir, ".pcast-doctor-write-probe")
		if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "data_dir", Status: "error", Message: "not writable: " + err.Error(),
			})
			res.OK = false
		} else if err := os.Remove(probe); err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "data_dir", Status: "error", Message: "cannot remove write probe: " + err.Error(),
			})
			res.OK = false
		} else {
			dataDirOK = true
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "data_dir", Status: "ok", Message: a.DataDir,
			})
		}
	}

	st := a.Store
	opened := false
	var openErr error
	if st == nil && dataDirOK {
		path := a.DBPath
		if path == "" {
			path = filepath.Join(a.DataDir, "pcast.db")
		}
		if a.OpenStore == nil {
			openErr = fmt.Errorf("store opener is not configured")
		} else {
			openedStore, err := a.OpenStore(ctx, path)
			openErr = err
			if err != nil {
				st = nil
			} else {
				st = openedStore
				opened = st != nil
			}
		}
	}
	if opened {
		defer func() { _ = st.Close() }()
	}

	if st == nil {
		message := "database not checked: data directory unavailable"
		if openErr != nil {
			message = openErr.Error()
		}
		res.Checks = append(res.Checks,
			model.DoctorCheck{Name: "database", Status: "error", Message: message},
			model.DoctorCheck{Name: "foreign_keys", Status: "error", Message: "not checked: database unavailable"},
		)
		res.OK = false
	} else {
		ver, err := st.SchemaVersion(ctx)
		if err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "database", Status: "error", Message: err.Error(),
			})
			res.OK = false
		} else {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "database", Status: "ok", Message: fmt.Sprintf("schema version %d", ver),
			})
		}
		fk, err := st.ForeignKeysEnabled(ctx)
		if err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "foreign_keys", Status: "error", Message: err.Error(),
			})
			res.OK = false
		} else if !fk {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "foreign_keys", Status: "error", Message: "foreign keys are disabled",
			})
			res.OK = false
		} else {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "foreign_keys", Status: "ok", Message: "enabled",
			})
		}
	}

	if a.Player == nil {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "player", Status: "warn", Message: "player is not configured",
		})
	} else {
		ref, err := a.Player.Resolve("", nil)
		if err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "player", Status: "warn", Message: err.Error(),
			})
		} else {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "player", Status: "ok", Message: fmt.Sprintf("%s (%s)", ref.Name, ref.Path),
			})
		}
	}

	return res, nil
}
