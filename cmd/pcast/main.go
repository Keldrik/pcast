package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Keldrik/pcast/internal/cli"
	"github.com/Keldrik/pcast/internal/model"
)

// Filled by -ldflags at release time.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := cli.Options{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Getenv: os.Getenv,
		Build: cli.BuildInfo{
			Version:   version,
			Commit:    commit,
			BuildDate: buildDate,
		},
	}
	root := cli.NewRoot(opts)
	root.SetArgs(args)
	root.SetContext(ctx)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return model.ExitOK
	}

	jsonMode := hasJSONFlag(args)

	// Partial latest already wrote stdout + diagnostics; exit 4 without error envelope.
	if cli.IsPartial(err) {
		return model.ExitPartialLatest
	}

	// Map plain cobra usage errors into typed invalid_argument.
	if !isTyped(err) && isCobraUsage(err) {
		err = model.InvalidArgument(err.Error())
	}

	cli.RenderError(os.Stderr, jsonMode, err)
	return cli.ExitCode(err)
}

func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
		if a == "--" {
			break
		}
	}
	return false
}

func isTyped(err error) bool {
	var ae *model.Error
	return errors.As(err, &ae)
}

func isCobraUsage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unknown command"):
		return true
	case strings.HasPrefix(msg, "unknown flag"):
		return true
	case strings.HasPrefix(msg, "invalid argument"):
		return true
	case strings.Contains(msg, "accepts "):
		return true
	case strings.Contains(msg, "requires "):
		return true
	case strings.Contains(msg, "required flag"):
		return true
	case errors.Is(err, context.Canceled):
		return false
	default:
		// Flag parse errors etc.
		if strings.Contains(msg, "flag") || strings.Contains(msg, "arg") {
			return true
		}
		return false
	}
}
