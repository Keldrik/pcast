package app

import (
	"context"
	"fmt"
	"os"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/platform"
)

// Doctor runs non-destructive diagnostics.
func (a *App) Doctor(ctx context.Context) (model.DoctorResult, error) {
	dir := a.DataDir
	res := model.DoctorResult{
		DataDir: dir,
		Checks:  []model.DoctorCheck{},
		OK:      true,
	}

	// Data directory resolve/create/write.
	if dir == "" {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "data_dir", Status: "error", Message: "data directory is not configured",
		})
		res.OK = false
	} else {
		if err := platform.EnsureDataDir(dir); err != nil {
			res.Checks = append(res.Checks, model.DoctorCheck{
				Name: "data_dir", Status: "error", Message: err.Error(),
			})
			res.OK = false
		} else {
			// Probe write
			probe := dir + string(os.PathSeparator) + ".pcast-doctor-write-probe"
			if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
				res.Checks = append(res.Checks, model.DoctorCheck{
					Name: "data_dir", Status: "error", Message: "not writable: " + err.Error(),
				})
				res.OK = false
			} else {
				_ = os.Remove(probe)
				res.Checks = append(res.Checks, model.DoctorCheck{
					Name: "data_dir", Status: "ok", Message: dir,
				})
			}
		}
	}

	// Database / schema
	if a.Store == nil {
		res.Checks = append(res.Checks, model.DoctorCheck{
			Name: "database", Status: "error", Message: "store is not configured",
		})
		res.OK = false
	} else {
		ver, err := a.Store.SchemaVersion(ctx)
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
		fk, err := a.Store.ForeignKeysEnabled(ctx)
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

	// Player
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
