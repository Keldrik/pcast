package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	dirName  = "pcast"
	dbName   = "pcast.db"
	lockName = "pcast.lock"
)

// Paths holds resolved data-directory locations.
type Paths struct {
	DataDir  string
	DBPath   string
	LockPath string
}

// ResolvePaths picks the data directory in order: explicit flag, PCAST_HOME, platform default.
func ResolvePaths(dataDirFlag string, getenv func(string) string, userHome func() (string, error)) (Paths, error) {
	var dir string
	switch {
	case dataDirFlag != "":
		dir = dataDirFlag
	case getenv != nil && getenv("PCAST_HOME") != "":
		dir = getenv("PCAST_HOME")
	default:
		home, err := defaultDataDir(getenv, userHome)
		if err != nil {
			return Paths{}, err
		}
		dir = home
	}
	dir = filepath.Clean(dir)
	return Paths{
		DataDir:  dir,
		DBPath:   filepath.Join(dir, dbName),
		LockPath: filepath.Join(dir, lockName),
	}, nil
}

func defaultDataDir(getenv func(string) string, userHome func() (string, error)) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if userHome == nil {
		userHome = os.UserHomeDir
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := userHome()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", dirName), nil
	case "windows":
		if base := getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, dirName), nil
		}
		home, err := userHome()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, "AppData", "Local", dirName), nil
	default:
		if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, dirName), nil
		}
		home, err := userHome()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", dirName), nil
	}
}

// EnsureDataDir creates the data directory with owner-only permissions when supported.
func EnsureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data directory %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat data directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("data path %s is not a directory", dir)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("protect data directory %s: %w", dir, err)
		}
	}
	return nil
}
