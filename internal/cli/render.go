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
	for _, line := range []string{
		fmt.Sprintf("Podcast %d %s\n", r.Podcast.ID, state),
		fmt.Sprintf("  Title:    %s\n", r.Podcast.Title),
	} {
		if err := writeString(w, line); err != nil {
			return err
		}
	}
	if r.Podcast.Alias != nil {
		if err := writeString(w, fmt.Sprintf("  Alias:    %s\n", *r.Podcast.Alias)); err != nil {
			return err
		}
	}
	if err := writeString(w, fmt.Sprintf("  Feed URL: %s\n", r.Podcast.FeedURL)); err != nil {
		return err
	}
	if r.Podcast.ResolvedURL != r.Podcast.FeedURL {
		if err := writeString(w, fmt.Sprintf("  Resolved: %s\n", r.Podcast.ResolvedURL)); err != nil {
			return err
		}
	}
	return writeString(w, fmt.Sprintf("  Episodes: %d\n", r.EpisodeCount))
}

// RenderList writes list result.
func RenderList(w io.Writer, jsonMode bool, r model.ListResult) error {
	if jsonMode {
		return WriteJSON(w, "list", r)
	}
	if len(r.Podcasts) == 0 {
		return writeString(w, "No subscriptions.\n")
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if err := writeString(tw, "ID\tNAME\tTITLE\tEPISODES\tUNPLAYED\tLAST SUCCESS\tLAST ERROR\n"); err != nil {
		return err
	}
	for _, p := range r.Podcasts {
		name := "-"
		if p.Alias != nil {
			name = *p.Alias
		}
		lastErr := "-"
		if p.LastError != nil && *p.LastError != "" {
			lastErr = truncate(*p.LastError, 40)
		}
		if err := writeString(tw, fmt.Sprintf("%d\t%s\t%s\t%d\t%d\t%s\t%s\n",
			p.ID, name, p.Title, p.EpisodeCount, p.UnplayedCount,
			fmtTime(p.LastSuccessAt), lastErr)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// RenderRemove writes remove result.
func RenderRemove(w io.Writer, jsonMode bool, r model.RemoveResult) error {
	if jsonMode {
		return WriteJSON(w, "remove", r)
	}
	return writeString(w, fmt.Sprintf("Removed podcast %d (%s)\n", r.Podcast.ID, podcastLabel(r.Podcast)))
}

// RenderLatest writes latest result.
func RenderLatest(w io.Writer, jsonMode bool, r model.LatestResult) error {
	if jsonMode {
		return WriteJSON(w, "latest", r)
	}
	if len(r.Episodes) == 0 {
		if r.Partial {
			return writeString(w, "No new episodes. Some feeds failed.\n")
		}
		return writeString(w, "No new episodes.\n")
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if err := writeString(tw, "ID\tPODCAST\tPUBLISHED\tTITLE\n"); err != nil {
		return err
	}
	for _, e := range r.Episodes {
		if err := writeString(tw, fmt.Sprintf("%d\t%s\t%s\t%s\n",
			e.ID, episodePodcastLabel(e), fmtTime(e.PublishedAt), e.Title)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// RenderLatestDiagnostics writes partial-failure notes to stderr.
func RenderLatestDiagnostics(stderr io.Writer, _ bool, r model.LatestResult) error {
	if !r.Partial || len(r.Failures) == 0 {
		return nil
	}
	for _, f := range r.Failures {
		if err := writeString(stderr, fmt.Sprintf("warning: podcast %d: %s\n", f.PodcastID, f.Message)); err != nil {
			return err
		}
	}
	return nil
}

// RenderEpisodes writes episodes list.
func RenderEpisodes(w io.Writer, jsonMode bool, r model.EpisodesResult) error {
	if jsonMode {
		return WriteJSON(w, "episodes", r)
	}
	if len(r.Episodes) == 0 {
		return writeString(w, "No episodes.\n")
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if err := writeString(tw, "ID\tPODCAST\tPUBLISHED\tDURATION\tPLAYED\tTITLE\n"); err != nil {
		return err
	}
	for _, e := range r.Episodes {
		if err := writeString(tw, fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\n",
			e.ID, episodePodcastLabel(e), fmtTime(e.PublishedAt),
			fmtDuration(e.DurationSeconds), playedLabel(e), e.Title)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// RenderEpisode writes episode detail.
func RenderEpisode(w io.Writer, jsonMode bool, r model.EpisodeResult) error {
	if jsonMode {
		return WriteJSON(w, "episode", r)
	}
	e := r.Episode
	lines := []string{
		fmt.Sprintf("Episode %d\n", e.ID),
		fmt.Sprintf("  Podcast:     %s (id %d)\n", episodePodcastLabel(e), e.PodcastID),
		fmt.Sprintf("  Title:       %s\n", e.Title),
		fmt.Sprintf("  Published:   %s\n", fmtTimeFull(e.PublishedAt)),
		fmt.Sprintf("  Duration:    %s\n", fmtDuration(e.DurationSeconds)),
		fmt.Sprintf("  Played:      %s\n", playedLabel(e)),
		fmt.Sprintf("  Play count:  %d\n", e.PlayCount),
		fmt.Sprintf("  Media URL:   %s\n", e.EnclosureURL),
	}
	if e.Description != nil && *e.Description != "" {
		lines = append(lines, fmt.Sprintf("  Description: %s\n", strings.TrimSpace(*e.Description)))
	}
	for _, line := range lines {
		if err := writeString(w, line); err != nil {
			return err
		}
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
	return writeString(w, fmt.Sprintf("Episode %d marked %s\n", r.Episode.ID, state))
}

// RenderPlay writes play result.
func RenderPlay(w io.Writer, jsonMode bool, r model.PlayResult) error {
	if jsonMode {
		return WriteJSON(w, "play", r)
	}
	if err := writeString(w, fmt.Sprintf("Played episode %d with %s\n", r.Episode.ID, r.Player)); err != nil {
		return err
	}
	if r.Marked {
		return writeString(w, "Marked as played.\n")
	}
	return nil
}

// RenderDoctor writes doctor result.
func RenderDoctor(w io.Writer, jsonMode bool, r model.DoctorResult) error {
	if jsonMode {
		return WriteJSON(w, "doctor", r)
	}
	if err := writeString(w, fmt.Sprintf("Data directory: %s\n", r.DataDir)); err != nil {
		return err
	}
	for _, c := range r.Checks {
		if err := writeString(w, fmt.Sprintf("  [%s] %s: %s\n", strings.ToUpper(c.Status), c.Name, c.Message)); err != nil {
			return err
		}
	}
	if r.OK {
		return writeString(w, "OK\n")
	}
	return writeString(w, "FAILED\n")
}

// RenderVersion writes version info.
func RenderVersion(w io.Writer, jsonMode bool, v model.VersionInfo) error {
	if jsonMode {
		return WriteJSON(w, "version", v)
	}
	for _, line := range []string{
		fmt.Sprintf("pcast %s\n", v.Version),
		fmt.Sprintf("  commit:     %s\n", v.Commit),
		fmt.Sprintf("  built:      %s\n", v.BuildDate),
		fmt.Sprintf("  go:         %s\n", v.GoVersion),
		fmt.Sprintf("  os/arch:    %s/%s\n", v.OS, v.Arch),
	} {
		if err := writeString(w, line); err != nil {
			return err
		}
	}
	return nil
}

func writeString(w io.Writer, s string) error {
	_, err := io.WriteString(w, s)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
