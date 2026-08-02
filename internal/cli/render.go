package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Keldrik/pcast/internal/model"
)

// Envelope is the stable JSON success envelope.
type Envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Command       string `json:"command"`
	Data          any    `json:"data"`
}

// ErrorEnvelope is the JSON error document written to stderr.
type ErrorEnvelope struct {
	SchemaVersion int `json:"schema_version"`
	Error         struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// WriteJSON writes a success envelope to w.
func WriteJSON(w io.Writer, command string, data any) error {
	env := Envelope{
		SchemaVersion: model.SchemaVersion,
		Command:       command,
		Data:          data,
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// WriteJSONError writes an error envelope to w.
func WriteJSONError(w io.Writer, err error) error {
	var env ErrorEnvelope
	env.SchemaVersion = model.SchemaVersion
	env.Error.Code = model.CodeOf(err)
	env.Error.Message = humanErrorMessage(err)
	env.Error.Details = model.DetailsOf(err)
	if env.Error.Details == nil {
		env.Error.Details = map[string]any{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

func humanErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if ae, ok := err.(*model.Error); ok {
		return ae.Message
	}
	return err.Error()
}

// RenderError writes a human or JSON error to stderr.
func RenderError(stderr io.Writer, jsonMode bool, err error) {
	if err == nil {
		return
	}
	if jsonMode {
		_ = WriteJSONError(stderr, err)
		return
	}
	fmt.Fprintf(stderr, "error: %s\n", humanErrorMessage(err))
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func fmtTimeFull(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func fmtDuration(sec *int) string {
	if sec == nil {
		return "-"
	}
	s := *sec
	h := s / 3600
	m := (s % 3600) / 60
	r := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, r)
	}
	return fmt.Sprintf("%d:%02d", m, r)
}

func podcastLabel(p model.Podcast) string {
	if p.Alias != nil && *p.Alias != "" {
		return *p.Alias
	}
	return p.Title
}

func episodePodcastLabel(e model.Episode) string {
	if e.PodcastAlias != nil && *e.PodcastAlias != "" {
		return *e.PodcastAlias
	}
	if e.PodcastTitle != "" {
		return e.PodcastTitle
	}
	return fmt.Sprintf("podcast:%d", e.PodcastID)
}

func playedLabel(e model.Episode) string {
	if e.IsPlayed() {
		return "yes"
	}
	return "no"
}

// RenderAdd writes add result.
func RenderAdd(w io.Writer, jsonMode bool, r model.AddResult) error {
	if jsonMode {
		return WriteJSON(w, "add", r)
	}
	state := "exists"
	if r.Created {
		state = "created"
	}
	fmt.Fprintf(w, "Podcast %d %s\n", r.Podcast.ID, state)
	fmt.Fprintf(w, "  Title:    %s\n", r.Podcast.Title)
	if r.Podcast.Alias != nil {
		fmt.Fprintf(w, "  Alias:    %s\n", *r.Podcast.Alias)
	}
	fmt.Fprintf(w, "  Feed URL: %s\n", r.Podcast.FeedURL)
	if r.Podcast.ResolvedURL != r.Podcast.FeedURL {
		fmt.Fprintf(w, "  Resolved: %s\n", r.Podcast.ResolvedURL)
	}
	fmt.Fprintf(w, "  Episodes: %d\n", r.EpisodeCount)
	return nil
}

// RenderList writes list result.
func RenderList(w io.Writer, jsonMode bool, r model.ListResult) error {
	if jsonMode {
		return WriteJSON(w, "list", r)
	}
	if len(r.Podcasts) == 0 {
		fmt.Fprintln(w, "No subscriptions.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTITLE\tEPISODES\tUNPLAYED\tLAST SUCCESS\tLAST ERROR")
	for _, p := range r.Podcasts {
		name := "-"
		if p.Alias != nil {
			name = *p.Alias
		}
		lastErr := "-"
		if p.LastError != nil && *p.LastError != "" {
			lastErr = truncate(*p.LastError, 40)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%s\t%s\n",
			p.ID, name, p.Title, p.EpisodeCount, p.UnplayedCount,
			fmtTime(p.LastSuccessAt), lastErr)
	}
	return tw.Flush()
}

// RenderRemove writes remove result.
func RenderRemove(w io.Writer, jsonMode bool, r model.RemoveResult) error {
	if jsonMode {
		return WriteJSON(w, "remove", r)
	}
	fmt.Fprintf(w, "Removed podcast %d (%s)\n", r.Podcast.ID, podcastLabel(r.Podcast))
	return nil
}

// RenderLatest writes latest result.
func RenderLatest(w io.Writer, jsonMode bool, r model.LatestResult) error {
	if jsonMode {
		return WriteJSON(w, "latest", r)
	}
	if len(r.Episodes) == 0 {
		if r.Partial {
			fmt.Fprintln(w, "No new episodes. Some feeds failed.")
		} else {
			fmt.Fprintln(w, "No new episodes.")
		}
	} else {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tPODCAST\tPUBLISHED\tTITLE")
		for _, e := range r.Episodes {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n",
				e.ID, episodePodcastLabel(e), fmtTime(e.PublishedAt), e.Title)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// RenderLatestDiagnostics writes partial-failure notes to stderr.
func RenderLatestDiagnostics(stderr io.Writer, jsonMode bool, r model.LatestResult) {
	if !r.Partial || len(r.Failures) == 0 {
		return
	}
	if jsonMode {
		// failures already in JSON body; still emit concise stderr diagnostic
	}
	for _, f := range r.Failures {
		fmt.Fprintf(stderr, "warning: podcast %d: %s\n", f.PodcastID, f.Message)
	}
}

// RenderEpisodes writes episodes list.
func RenderEpisodes(w io.Writer, jsonMode bool, r model.EpisodesResult) error {
	if jsonMode {
		return WriteJSON(w, "episodes", r)
	}
	if len(r.Episodes) == 0 {
		fmt.Fprintln(w, "No episodes.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPODCAST\tPUBLISHED\tDURATION\tPLAYED\tTITLE")
	for _, e := range r.Episodes {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
			e.ID, episodePodcastLabel(e), fmtTime(e.PublishedAt),
			fmtDuration(e.DurationSeconds), playedLabel(e), e.Title)
	}
	return tw.Flush()
}

// RenderEpisode writes episode detail.
func RenderEpisode(w io.Writer, jsonMode bool, r model.EpisodeResult) error {
	if jsonMode {
		return WriteJSON(w, "episode", r)
	}
	e := r.Episode
	fmt.Fprintf(w, "Episode %d\n", e.ID)
	fmt.Fprintf(w, "  Podcast:     %s (id %d)\n", episodePodcastLabel(e), e.PodcastID)
	fmt.Fprintf(w, "  Title:       %s\n", e.Title)
	fmt.Fprintf(w, "  Published:   %s\n", fmtTimeFull(e.PublishedAt))
	fmt.Fprintf(w, "  Duration:    %s\n", fmtDuration(e.DurationSeconds))
	fmt.Fprintf(w, "  Played:      %s\n", playedLabel(e))
	fmt.Fprintf(w, "  Play count:  %d\n", e.PlayCount)
	fmt.Fprintf(w, "  Media URL:   %s\n", e.EnclosureURL)
	if e.Description != nil && *e.Description != "" {
		fmt.Fprintf(w, "  Description: %s\n", strings.TrimSpace(*e.Description))
	}
	return nil
}

// RenderMark writes mark result.
func RenderMark(w io.Writer, jsonMode bool, r model.MarkResult) error {
	if jsonMode {
		return WriteJSON(w, "mark", r)
	}
	state := "unplayed"
	if r.Episode.IsPlayed() {
		state = "played"
	}
	fmt.Fprintf(w, "Episode %d marked %s\n", r.Episode.ID, state)
	return nil
}

// RenderPlay writes play result.
func RenderPlay(w io.Writer, jsonMode bool, r model.PlayResult) error {
	if jsonMode {
		return WriteJSON(w, "play", r)
	}
	fmt.Fprintf(w, "Played episode %d with %s\n", r.Episode.ID, r.Player)
	if r.Marked {
		fmt.Fprintln(w, "Marked as played.")
	}
	return nil
}

// RenderDoctor writes doctor result.
func RenderDoctor(w io.Writer, jsonMode bool, r model.DoctorResult) error {
	if jsonMode {
		return WriteJSON(w, "doctor", r)
	}
	fmt.Fprintf(w, "Data directory: %s\n", r.DataDir)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "  [%s] %s: %s\n", strings.ToUpper(c.Status), c.Name, c.Message)
	}
	if r.OK {
		fmt.Fprintln(w, "OK")
	} else {
		fmt.Fprintln(w, "FAILED")
	}
	return nil
}

// RenderVersion writes version info.
func RenderVersion(w io.Writer, jsonMode bool, v model.VersionInfo) error {
	if jsonMode {
		return WriteJSON(w, "version", v)
	}
	fmt.Fprintf(w, "pcast %s\n", v.Version)
	fmt.Fprintf(w, "  commit:     %s\n", v.Commit)
	fmt.Fprintf(w, "  built:      %s\n", v.BuildDate)
	fmt.Fprintf(w, "  go:         %s\n", v.GoVersion)
	fmt.Fprintf(w, "  os/arch:    %s/%s\n", v.OS, v.Arch)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
