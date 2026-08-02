package player_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/player"
)

func TestResolvePrecedence(t *testing.T) {
	r := player.New()
	calls := []string{}
	r.LookPath = func(file string) (string, error) {
		calls = append(calls, file)
		if file == "mpv" {
			return "/usr/bin/mpv", nil
		}
		return "", exec.ErrNotFound
	}
	r.Getenv = func(string) string { return "" }
	res, err := r.Resolve("", nil)
	if err != nil || res.Path != "/usr/bin/mpv" {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	r.Getenv = func(k string) string {
		if k == "PCAST_PLAYER" {
			return "custom"
		}
		return ""
	}
	r.LookPath = func(file string) (string, error) {
		if file == "custom" {
			return "/bin/custom", nil
		}
		return "", exec.ErrNotFound
	}
	res, err = r.Resolve("", []string{"--foo"})
	if err != nil || res.Path != "/bin/custom" || len(res.Args) != 1 {
		t.Fatalf("%+v err=%v", res, err)
	}

	r.LookPath = func(file string) (string, error) {
		if file == "explicit" {
			return "/bin/explicit", nil
		}
		return "", exec.ErrNotFound
	}
	res, err = r.Resolve("explicit", []string{"a", "b"})
	if err != nil || res.Path != "/bin/explicit" {
		t.Fatalf("%+v err=%v", res, err)
	}
	argv := player.BuildArgv(res, "https://cdn.example.com/a b.mp3")
	if len(argv) != 4 || argv[3] != "https://cdn.example.com/a b.mp3" {
		t.Fatalf("argv=%v", argv)
	}
}

func TestPlaySuccessAndFailure(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("no true on windows")
		}
		t.Fatal(err)
	}
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false")
	}

	var sink bytes.Buffer
	r := player.New()
	r.Stdout = &sink
	r.Stderr = &sink
	r.Stdin = bytes.NewReader(nil)

	out, err := r.Play(context.Background(), player.ResolveResult{Path: truePath, Name: "true"}, "https://x/a.mp3")
	if err != nil || out.ExitStatus != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}

	_, err = r.Play(context.Background(), player.ResolveResult{Path: falsePath, Name: "false"}, "https://x/a.mp3")
	if err == nil || model.CodeOf(err) != model.CodePlayerFailed {
		t.Fatalf("err=%v", err)
	}

	// Ensure JSON-ish stdout not contaminated (sink is separate)
	if bytes.Contains(sink.Bytes(), []byte(`"schema_version"`)) {
		t.Fatal("player wrote json envelope")
	}
}

func TestPlayCancel(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	r := player.New()
	r.Stdout = bytes.NewBuffer(nil)
	r.Stderr = bytes.NewBuffer(nil)
	r.Stdin = bytes.NewReader(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Use Args for duration so the process runs long enough to hit the context deadline.
	_, err = r.Play(ctx, player.ResolveResult{Path: sleep, Args: []string{"30"}, Name: "sleep"}, "https://x")
	if err == nil {
		t.Fatal("expected cancel/fail")
	}
}

func TestMissingPlayer(t *testing.T) {
	r := player.New()
	r.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	r.Getenv = func(string) string { return "" }
	r.GOOS = "plan9"
	_, err := r.Resolve("", nil)
	if err == nil || model.CodeOf(err) != model.CodePlayerUnavailable {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveRejectsMissingConfiguredPath(t *testing.T) {
	r := player.New()
	r.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	r.Getenv = func(string) string { return "/missing/player" }
	if _, err := r.Resolve("", nil); model.CodeOf(err) != model.CodePlayerUnavailable {
		t.Fatalf("err=%v code=%s", err, model.CodeOf(err))
	}
	if _, err := r.Resolve("/missing/player", nil); model.CodeOf(err) != model.CodePlayerUnavailable {
		t.Fatalf("explicit err=%v code=%s", err, model.CodeOf(err))
	}
}

func TestHostileArgs(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	r := player.New()
	r.Stdout = bytes.NewBuffer(nil)
	r.Stderr = bytes.NewBuffer(nil)
	r.Stdin = bytes.NewReader(nil)
	args := []string{"--title", "foo; rm -rf /", "--audio-display=no"}
	url := "https://example.com/a.mp3?x=1&y=2;whoami"
	res := player.ResolveResult{Path: truePath, Args: args, Name: "true"}
	argv := player.BuildArgv(res, url)
	if strings.Join(argv, " ") != truePath+" --title foo; rm -rf / --audio-display=no "+url {
		// Just ensure elements preserved literally
		if argv[2] != "foo; rm -rf /" || argv[len(argv)-1] != url {
			t.Fatalf("argv=%v", argv)
		}
	}
	_, err = r.Play(context.Background(), res, url)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLookPathExplicitRelative(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "myplayer")
	// create an executable path and make the injected lookup confirm it.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := player.New()
	r.LookPath = func(name string) (string, error) {
		if name == bin {
			return bin, nil
		}
		return "", exec.ErrNotFound
	}
	res, err := r.Resolve(bin, nil)
	if err != nil || res.Path != bin {
		t.Fatalf("%+v err=%v", res, err)
	}
}
