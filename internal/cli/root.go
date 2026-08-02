package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/Keldrik/pcast/internal/app"
	"github.com/Keldrik/pcast/internal/feed"
	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/platform"
	"github.com/Keldrik/pcast/internal/player"
	"github.com/Keldrik/pcast/internal/store"
)

// BuildInfo is injected by the main package / linker.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Options configures the root command.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Getenv func(string) string
	Clock  app.Clock
	Build  BuildInfo
	// Optional test overrides.
	OpenStore func(ctx context.Context, path string) (app.StorePort, error)
	NewFeeds  func(userAgent string) app.FeedClient
	NewPlayer func() app.Player
	NewLock   func(path string) app.Locker
}

// Root holds global flag state and shared deps.
type Root struct {
	opts    Options
	jsonOut bool
	dataDir string

	// set during PersistentPreRun for commands that need it
	paths       platform.Paths
	st          app.StorePort
	application *app.App
	opened      bool
}

// NewRoot constructs the cobra command tree.
func NewRoot(opts Options) *cobra.Command {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Clock == nil {
		opts.Clock = app.RealClock{}
	}
	if opts.Build.Version == "" {
		opts.Build.Version = "dev"
	}
	if opts.Build.Commit == "" {
		opts.Build.Commit = "unknown"
	}
	if opts.Build.BuildDate == "" {
		opts.Build.BuildDate = "unknown"
	}

	r := &Root{opts: opts}

	cmd := &cobra.Command{
		Use:           "pcast",
		Short:         "Local-first podcast manager and player",
		Long:          "pcast subscribes to RSS/Atom podcast feeds, tracks episodes locally, and streams them through an installed media player.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return r.persistentPreRun(cmd)
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			return r.close()
		},
	}

	cmd.PersistentFlags().BoolVar(&r.jsonOut, "json", false, "emit machine-readable JSON")
	cmd.PersistentFlags().StringVar(&r.dataDir, "data-dir", "", "directory for database and lock file")

	cmd.AddCommand(r.cmdVersion())
	cmd.AddCommand(r.cmdAdd())
	cmd.AddCommand(r.cmdRemove())
	cmd.AddCommand(r.cmdList())
	cmd.AddCommand(r.cmdLatest())
	cmd.AddCommand(r.cmdEpisodes())
	cmd.AddCommand(r.cmdEpisode())
	cmd.AddCommand(r.cmdPlay())
	cmd.AddCommand(r.cmdMark())
	cmd.AddCommand(r.cmdDoctor())

	// Default version template for --version
	cmd.Version = opts.Build.Version
	cmd.SetVersionTemplate("{{.Version}}\n")

	cmd.SetOut(opts.Stdout)
	cmd.SetErr(opts.Stderr)
	cmd.SetIn(opts.Stdin)

	return cmd
}

func (r *Root) versionInfo() model.VersionInfo {
	goVersion := runtime.Version()
	if bi, ok := debug.ReadBuildInfo(); ok && bi.GoVersion != "" {
		goVersion = bi.GoVersion
	}
	return model.VersionInfo{
		Version:   r.opts.Build.Version,
		Commit:    r.opts.Build.Commit,
		BuildDate: r.opts.Build.BuildDate,
		GoVersion: goVersion,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

func (r *Root) persistentPreRun(cmd *cobra.Command) error {
	// Skip heavy init for help/version-only.
	name := cmd.Name()
	if name == "help" || name == "completion" {
		return nil
	}
	// version command does not need store
	needsStore := name != "version" && cmd.Parent() != nil

	paths, err := platform.ResolvePaths(r.dataDir, r.opts.Getenv, os.UserHomeDir)
	if err != nil {
		return model.Storage("resolve data directory", err)
	}
	r.paths = paths

	if !needsStore && name == "pcast" {
		return nil
	}
	if name == "version" {
		return nil
	}

	if err := platform.EnsureDataDir(paths.DataDir); err != nil {
		return model.Storage("create data directory", err)
	}

	open := r.opts.OpenStore
	if open == nil {
		open = func(ctx context.Context, path string) (app.StorePort, error) {
			return store.Open(ctx, path)
		}
	}
	st, err := open(cmd.Context(), paths.DBPath)
	if err != nil {
		return err
	}
	r.st = st
	r.opened = true

	ua := fmt.Sprintf("pcast/%s", r.opts.Build.Version)
	var feeds app.FeedClient
	if r.opts.NewFeeds != nil {
		feeds = r.opts.NewFeeds(ua)
	} else {
		feeds = &feedClientAdapter{c: feed.NewClient(ua)}
	}

	var play app.Player
	if r.opts.NewPlayer != nil {
		play = r.opts.NewPlayer()
	} else {
		play = &playerAdapter{r: player.New()}
	}

	var lock app.Locker
	if r.opts.NewLock != nil {
		lock = r.opts.NewLock(paths.LockPath)
	} else {
		lock = platform.NewLock(paths.LockPath)
	}

	r.application = &app.App{
		Store:   st,
		Feeds:   feeds,
		Player:  play,
		Lock:    lock,
		Clock:   r.opts.Clock,
		DataDir: paths.DataDir,
		Version: r.versionInfo(),
	}
	return nil
}

func (r *Root) close() error {
	if r.opened && r.st != nil {
		err := r.st.Close()
		r.opened = false
		return err
	}
	return nil
}

func (r *Root) appOrErr() (*app.App, error) {
	if r.application == nil {
		return nil, model.Internal("application not initialized", nil)
	}
	return r.application, nil
}

// Adapters

type feedClientAdapter struct {
	c *feed.Client
}

func (a *feedClientAdapter) Fetch(ctx context.Context, opts app.FetchOpts) (model.ParsedFeed, error) {
	return a.c.Fetch(ctx, feed.FetchOptions{
		URL:          opts.URL,
		ETag:         opts.ETag,
		LastModified: opts.LastModified,
	})
}

type playerAdapter struct {
	r *player.Runner
}

func (a *playerAdapter) Resolve(explicit string, extraArgs []string) (app.PlayerRef, error) {
	res, err := a.r.Resolve(explicit, extraArgs)
	if err != nil {
		return app.PlayerRef{}, err
	}
	return app.PlayerRef{
		Path:     res.Path,
		Args:     res.Args,
		IsOpener: res.IsOpener,
		Name:     res.Name,
	}, nil
}

func (a *playerAdapter) Play(ctx context.Context, ref app.PlayerRef, enclosureURL string) (app.PlayOutcome, error) {
	out, err := a.r.Play(ctx, player.ResolveResult{
		Path:     ref.Path,
		Args:     ref.Args,
		IsOpener: ref.IsOpener,
		Name:     ref.Name,
	}, enclosureURL)
	return app.PlayOutcome{
		Player:     out.Player,
		ExitStatus: out.ExitStatus,
		HandOff:    out.HandOff,
	}, err
}
