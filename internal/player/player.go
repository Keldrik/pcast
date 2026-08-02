package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Keldrik/pcast/internal/model"
)

// LookPathFunc locates an executable on PATH.
type LookPathFunc func(file string) (string, error)

// CommandFunc constructs a command.
type CommandFunc func(ctx context.Context, name string, arg ...string) *exec.Cmd

// Runner plays enclosure URLs via an external process.
type Runner struct {
	LookPath LookPathFunc
	Command  CommandFunc
	Getenv   func(string) string
	Stdout   io.Writer // player stdout destination (never command JSON stdout)
	Stderr   io.Writer
	Stdin    io.Reader
	GOOS     string
}

// New returns a Runner with process defaults.
func New() *Runner {
	return &Runner{
		LookPath: exec.LookPath,
		Command:  exec.CommandContext,
		Getenv:   os.Getenv,
		Stdout:   os.Stderr, // keep player chatter off command stdout
		Stderr:   os.Stderr,
		Stdin:    os.Stdin,
		GOOS:     runtime.GOOS,
	}
}

// ResolveResult describes the chosen player.
type ResolveResult struct {
	Path     string
	Args     []string
	IsOpener bool
	Name     string
}

// Resolve picks a player in documented order.
func (r *Runner) Resolve(explicit string, extraArgs []string) (ResolveResult, error) {
	look := r.LookPath
	if look == nil {
		look = exec.LookPath
	}
	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}

	try := func(name string, isOpener bool) (ResolveResult, bool) {
		path, err := look(name)
		if err != nil {
			return ResolveResult{}, false
		}
		return ResolveResult{
			Path:     path,
			Args:     append([]string{}, extraArgs...),
			IsOpener: isOpener,
			Name:     name,
		}, true
	}

	if explicit != "" {
		path, err := look(explicit)
		if err != nil {
			return ResolveResult{}, model.PlayerUnavailable(fmt.Sprintf("player %q not found", explicit))
		}
		return ResolveResult{Path: path, Args: append([]string{}, extraArgs...), Name: explicit}, nil
	}

	if env := strings.TrimSpace(getenv("PCAST_PLAYER")); env != "" {
		path, err := look(env)
		if err != nil {
			return ResolveResult{}, model.PlayerUnavailable(fmt.Sprintf("PCAST_PLAYER %q not found", env))
		}
		return ResolveResult{Path: path, Args: append([]string{}, extraArgs...), Name: env}, nil
	}

	for _, name := range []string{"mpv", "vlc", "ffplay"} {
		if res, ok := try(name, false); ok {
			return res, nil
		}
	}

	// Platform opener fallback.
	switch goos {
	case "darwin":
		if res, ok := try("open", true); ok {
			// open -W waits; without -W hand-off is immediate. Spec: successful hand-off marks played.
			// Use plain open without -W for hand-off semantics.
			return res, nil
		}
	case "windows":
		// cmd /c start is a shell; avoid. Use rundll32 url.dll,FileProtocolHandler
		if path, err := look("rundll32"); err == nil {
			return ResolveResult{
				Path:     path,
				Args:     append([]string{"url.dll,FileProtocolHandler"}, extraArgs...),
				IsOpener: true,
				Name:     "rundll32",
			}, nil
		}
	default:
		if res, ok := try("xdg-open", true); ok {
			return res, nil
		}
	}

	return ResolveResult{}, model.PlayerUnavailable("no media player found (tried mpv, vlc, ffplay, and platform opener)")
}

// PlayResult is the outcome of running the player.
type PlayResult struct {
	Player     string
	ExitStatus int
	HandOff    bool
}

// Play starts the player with args... + URL and waits (unless opener hand-off completes quickly).
func (r *Runner) Play(ctx context.Context, res ResolveResult, enclosureURL string) (PlayResult, error) {
	if res.Path == "" {
		return PlayResult{}, model.PlayerUnavailable("player path is empty")
	}
	cmdFn := r.Command
	if cmdFn == nil {
		cmdFn = exec.CommandContext
	}
	args := append(append([]string{}, res.Args...), enclosureURL)
	cmd := cmdFn(ctx, res.Path, args...)
	cmd.Stdin = r.Stdin
	// Never attach player stdout to the CLI data stream.
	if r.Stdout != nil {
		cmd.Stdout = r.Stdout
	} else {
		cmd.Stdout = os.Stderr
	}
	if r.Stderr != nil {
		cmd.Stderr = r.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		return PlayResult{}, model.PlayerFailed("start player", err)
	}
	err := cmd.Wait()
	status := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return PlayResult{Player: res.Name, ExitStatus: -1}, model.PlayerFailed("player cancelled", ctx.Err())
		}
		if errors.As(err, &ee) {
			status = ee.ExitCode()
			if res.IsOpener {
				// Some openers return non-zero even on hand-off; treat start success as enough only when Wait ok.
				return PlayResult{Player: res.Name, ExitStatus: status, HandOff: false}, model.PlayerFailed(fmt.Sprintf("player exited %d", status), err)
			}
			return PlayResult{Player: res.Name, ExitStatus: status}, model.PlayerFailed(fmt.Sprintf("player exited %d", status), err)
		}
		return PlayResult{Player: res.Name, ExitStatus: -1}, model.PlayerFailed("player failed", err)
	}
	return PlayResult{Player: res.Name, ExitStatus: status, HandOff: res.IsOpener}, nil
}

// BuildArgv returns the exact argument vector for tests.
func BuildArgv(res ResolveResult, enclosureURL string) []string {
	return append(append([]string{res.Path}, res.Args...), enclosureURL)
}
