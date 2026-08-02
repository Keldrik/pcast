package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePathsFlagPrecedence(t *testing.T) {
	p, err := ResolvePaths("/tmp/custom", func(string) string { return "/env/home" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.DataDir != filepath.Clean("/tmp/custom") {
		t.Fatalf("got %s", p.DataDir)
	}
	if p.DBPath != filepath.Join(p.DataDir, "pcast.db") {
		t.Fatalf("db=%s", p.DBPath)
	}
}

func TestResolvePathsPCASTHome(t *testing.T) {
	p, err := ResolvePaths("", func(k string) string {
		if k == "PCAST_HOME" {
			return "/from/env"
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.DataDir != filepath.Clean("/from/env") {
		t.Fatalf("got %s", p.DataDir)
	}
}

func TestResolvePathsPlatformDefault(t *testing.T) {
	home := "/home/tester"
	env := map[string]string{}
	p, err := ResolvePaths("", func(k string) string { return env[k] }, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "pcast")
	case "windows":
		want = filepath.Join(home, "AppData", "Local", "pcast")
	default:
		want = filepath.Join(home, ".local", "share", "pcast")
	}
	if p.DataDir != want {
		t.Fatalf("got %s want %s", p.DataDir, want)
	}
}

func TestResolvePathsXDG(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG only on unix-like")
	}
	p, err := ResolvePaths("", func(k string) string {
		if k == "XDG_DATA_HOME" {
			return "/xdg/data"
		}
		return ""
	}, func() (string, error) { return "/home/x", nil })
	if err != nil {
		t.Fatal(err)
	}
	if p.DataDir != filepath.Join("/xdg/data", "pcast") {
		t.Fatalf("got %s", p.DataDir)
	}
}

func TestEnsureDataDirTightensExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix directory permission bits")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDataDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("permissions=%o want 700", got)
	}
}

func TestEnsureDataDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "pcast")
	if err := EnsureDataDir(target); err != nil {
		t.Fatal(err)
	}
	info, err := filepath.Glob(target)
	if err != nil || len(info) != 1 {
		t.Fatalf("dir missing: %v", err)
	}
}
