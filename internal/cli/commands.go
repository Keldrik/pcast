package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Keldrik/pcast/internal/model"
)

func (r *Root) cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version and build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return RenderVersion(r.opts.Stdout, r.jsonOut, r.versionInfo())
		},
	}
}

func (r *Root) cmdAdd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "add <feed-url>",
		Short: "Subscribe to a podcast feed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.Add(cmd.Context(), args[0], name)
			if err != nil {
				return err
			}
			return RenderAdd(r.opts.Stdout, r.jsonOut, res)
		},
	}
	c.Flags().StringVar(&name, "name", "", "alias for the podcast")
	return c
}

func (r *Root) cmdRemove() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <podcast>",
		Short: "Remove a subscription and its local episodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.Remove(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return RenderRemove(r.opts.Stdout, r.jsonOut, res)
		},
	}
}

func (r *Root) cmdList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List subscriptions from local state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.List(cmd.Context())
			if err != nil {
				return err
			}
			return RenderList(r.opts.Stdout, r.jsonOut, res)
		},
	}
}

func (r *Root) cmdLatest() *cobra.Command {
	return &cobra.Command{
		Use:   "latest [podcast]",
		Short: "Check feeds and show newly discovered episodes",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			res, err := a.LatestLocked(cmd.Context(), selector, func(result model.LatestResult) error {
				return RenderLatest(r.opts.Stdout, r.jsonOut, result)
			})
			if err != nil {
				return err
			}
			if err := RenderLatestDiagnostics(r.opts.Stderr, r.jsonOut, res); err != nil {
				return model.Internal("write latest diagnostics", err)
			}
			if res.Partial {
				// Successful partial: output already written and acknowledged.
				// Return a typed error so main exits 4, but stdout already has data.
				return &partialError{result: res}
			}
			return nil
		},
	}
}

// partialError signals exit 4 after successful partial latest output.
type partialError struct {
	result model.LatestResult
}

func (e *partialError) Error() string {
	return fmt.Sprintf("partial success: %d feed(s) failed", len(e.result.Failures))
}

// AsModelError maps partialError for exit code handling.
func (e *partialError) ExitCode() int { return model.ExitPartialLatest }

func (r *Root) cmdEpisodes() *cobra.Command {
	var (
		limit    int
		offset   int
		all      bool
		played   bool
		unplayed bool
		newOnly  bool
		query    string
	)
	c := &cobra.Command{
		Use:   "episodes [podcast]",
		Short: "Query the cached episode catalog",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if played && unplayed {
				return model.InvalidArgument("--played and --unplayed are mutually exclusive")
			}
			if all && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")) {
				return model.InvalidArgument("--all is mutually exclusive with --limit and --offset")
			}
			f := model.EpisodeFilter{
				Limit:   limit,
				Offset:  offset,
				All:     all,
				Pending: newOnly,
				Query:   query,
			}
			if all {
				f.Limit = 0
				f.Offset = 0
			}
			if played {
				t := true
				f.Played = &t
			}
			if unplayed {
				t := false
				f.Played = &t
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.ListEpisodes(cmd.Context(), selector, f)
			if err != nil {
				return err
			}
			return RenderEpisodes(r.opts.Stdout, r.jsonOut, res)
		},
	}
	c.Flags().IntVar(&limit, "limit", 20, "maximum rows to return")
	c.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	c.Flags().BoolVar(&all, "all", false, "return all matching rows")
	c.Flags().BoolVar(&played, "played", false, "only played episodes")
	c.Flags().BoolVar(&unplayed, "unplayed", false, "only unplayed episodes")
	c.Flags().BoolVar(&newOnly, "new", false, "only pending/unannounced episodes")
	c.Flags().StringVar(&query, "query", "", "case-insensitive title/description search")
	return c
}

func (r *Root) cmdEpisode() *cobra.Command {
	return &cobra.Command{
		Use:   "episode <episode-id>",
		Short: "Show one cached episode in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.GetEpisode(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return RenderEpisode(r.opts.Stdout, r.jsonOut, res)
		},
	}
}

func (r *Root) cmdPlay() *cobra.Command {
	var (
		playerBin  string
		playerArgs []string
		noMark     bool
	)
	c := &cobra.Command{
		Use:   "play <episode-id>",
		Short: "Stream an episode through an external player",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.Play(cmd.Context(), args[0], model.PlaybackOpts{
				Player:       playerBin,
				PlayerArgs:   playerArgs,
				NoMarkPlayed: noMark,
			})
			if err != nil {
				// Still may want partial info; on error don't render success.
				return err
			}
			return RenderPlay(r.opts.Stdout, r.jsonOut, res)
		},
	}
	c.Flags().StringVar(&playerBin, "player", "", "player executable override")
	c.Flags().StringArrayVar(&playerArgs, "player-arg", nil, "literal argument for the player (repeatable)")
	c.Flags().BoolVar(&noMark, "no-mark-played", false, "do not mark the episode played")
	return c
}

func (r *Root) cmdMark() *cobra.Command {
	return &cobra.Command{
		Use:   "mark <episode-id> <played|unplayed>",
		Short: "Correct listening state without playback",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.Mark(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return RenderMark(r.opts.Stdout, r.jsonOut, res)
		},
	}
}

func (r *Root) cmdDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run local setup and automation diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := r.appOrErr()
			if err != nil {
				return err
			}
			res, err := a.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if err := RenderDoctor(r.opts.Stdout, r.jsonOut, res); err != nil {
				return err
			}
			if !res.OK {
				// The structured report is the result; signal the documented exit
				// status without emitting a second error document.
				return &doctorFailure{}
			}
			return nil
		},
	}
}

type doctorFailure struct{}

func (e *doctorFailure) Error() string { return "doctor checks failed" }
func (e *doctorFailure) ExitCode() int { return model.ExitStorage }

// ExitCode maps an error to a process exit status, including partial latest.
func ExitCode(err error) int {
	if err == nil {
		return model.ExitOK
	}
	if IsPartial(err) {
		return model.ExitPartialLatest
	}
	if IsDoctorFailure(err) {
		return model.ExitStorage
	}
	if _, ok := err.(*strconv.NumError); ok {
		return model.ExitUsage
	}
	var ae *model.Error
	if errors.As(err, &ae) {
		return model.ExitCode(err)
	}
	if IsCobraUsage(err) {
		return model.ExitUsage
	}
	return model.ExitCode(err)
}

// IsCobraUsage reports cobra/flag parse errors that should exit 2.
// Kept narrow: broad substring matches on "arg"/"flag" mis-classify real failures.
func IsCobraUsage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unknown command"),
		strings.HasPrefix(msg, "unknown flag"),
		strings.HasPrefix(msg, "bad flag"),
		strings.HasPrefix(msg, "invalid argument"),
		strings.HasPrefix(msg, "accepts "),
		strings.Contains(msg, "accepts at most"),
		strings.Contains(msg, "accepts between"),
		strings.Contains(msg, "requires at least"),
		strings.Contains(msg, "requires one of"),
		strings.Contains(msg, "required flag"),
		strings.HasPrefix(msg, "flag needs"),
		strings.HasPrefix(msg, "flag redefined"),
		strings.Contains(msg, "unknown shorthand flag"):
		return true
	default:
		return false
	}
}

// IsPartial reports whether err is a partial-latest sentinel.
// Callers must consume stdout on exit 4 when this is true (success-shaped body).
func IsPartial(err error) bool {
	var pe *partialError
	return errors.As(err, &pe)
}

// IsDoctorFailure reports a rendered doctor result that requires exit 5.
func IsDoctorFailure(err error) bool {
	var de *doctorFailure
	return errors.As(err, &de)
}
