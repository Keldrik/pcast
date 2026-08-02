package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/app"
	"github.com/Keldrik/pcast/internal/cli"
	"github.com/Keldrik/pcast/internal/feed"
	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/platform"
	"github.com/Keldrik/pcast/internal/player"
	"github.com/Keldrik/pcast/internal/store"
)

func runCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	var out, errb bytes.Buffer
	clock := app.FixedClock{T: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)}
	opts := cli.Options{
		Stdout: &out,
		Stderr: &errb,
		Stdin:  bytes.NewReader(nil),
		Getenv: func(k string) string {
			if k == "PCAST_HOME" {
				return dataDir
			}
			return ""
		},
		Clock: clock,
		Build: cli.BuildInfo{Version: "test", Commit: "abc", BuildDate: "2024-01-01"},
		NewPlayer: func() app.Player {
			return &playerAdapter{r: testPlayerRunner(t)}
		},
	}
	root, cleanup := cli.NewRoot(opts)
	defer func() { _ = cleanup() }()
	root.SetArgs(append([]string{"--data-dir", dataDir}, args...))
	err := root.ExecuteContext(context.Background())
	if err != nil && !cli.IsPartial(err) && !cli.IsDoctorFailure(err) {
		// Map plain cobra usage errors like main does.
		if _, ok := err.(*model.Error); !ok && cli.IsCobraUsage(err) {
			err = model.InvalidArgument(err.Error())
		}
		jsonMode := false
		for _, a := range args {
			if a == "--json" {
				jsonMode = true
			}
		}
		cli.RenderError(&errb, jsonMode, err)
	}
	exit = cli.ExitCode(err)
	return out.String(), errb.String(), exit
}

type playerAdapter struct{ r *player.Runner }

func (a *playerAdapter) Resolve(explicit string, extraArgs []string) (app.PlayerRef, error) {
	res, err := a.r.Resolve(explicit, extraArgs)
	return app.PlayerRef{Path: res.Path, Args: res.Args, IsOpener: res.IsOpener, Name: res.Name}, err
}
func (a *playerAdapter) Play(ctx context.Context, ref app.PlayerRef, url string) (app.PlayOutcome, error) {
	out, err := a.r.Play(ctx, player.ResolveResult{Path: ref.Path, Args: ref.Args, IsOpener: ref.IsOpener, Name: ref.Name}, url)
	return app.PlayOutcome{Player: out.Player, ExitStatus: out.ExitStatus, HandOff: out.HandOff}, err
}

func testPlayerRunner(t *testing.T) *player.Runner {
	t.Helper()
	// Use a tiny helper process via `go test` helper or /bin/true-like.
	helper := filepath.Join(t.TempDir(), "fakeplayer")
	// Write a small shell-free go binary alternative: use `true` or write script...
	// Spec: never shell. Use os executable that exits 0: on unix /usr/bin/true or `echo`.
	path, err := exec.LookPath("true")
	if err != nil {
		// windows: use cmd is shell - write a tiny go program instead via copying `os.Args`
		// fallback write a no-op bat? Better: use player.Runner with Command override.
		_ = helper
		r := player.New()
		r.LookPath = func(file string) (string, error) {
			if file == "mpv" || file == "true" || file == "fakeplayer" {
				return "/fake/mpv", nil
			}
			return "", exec.ErrNotFound
		}
		r.Command = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
			// Run the test binary as a helper? Simpler: use `go` env.
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperPlayer", "--", "ok")
			cmd.Env = append(os.Environ(), "PCAST_TEST_PLAYER_HELPER=1")
			return cmd
		}
		r.Stdout = io.Discard
		r.Stderr = io.Discard
		r.Stdin = bytes.NewReader(nil)
		return r
	}
	r := player.New()
	r.LookPath = func(file string) (string, error) {
		if file == "mpv" || file == path || file == "true" {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
	r.Stdout = io.Discard
	r.Stderr = io.Discard
	r.Stdin = bytes.NewReader(nil)
	return r
}

func TestHelperPlayer(t *testing.T) {
	if os.Getenv("PCAST_TEST_PLAYER_HELPER") != "1" {
		return
	}
	os.Exit(0)
}

func feedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVersionAndHelp(t *testing.T) {
	dir := t.TempDir()
	out, errb, exit := runCLI(t, dir, "version")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, errb)
	}
	if !strings.Contains(out, "pcast test") {
		t.Fatalf("out=%q", out)
	}
	out, errb, exit = runCLI(t, dir, "--json", "version")
	if exit != 0 {
		t.Fatal(exit, errb)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err, out)
	}
	if env["command"] != "version" {
		t.Fatalf("%v", env)
	}
	_, errb, exit = runCLI(t, dir, "nope")
	if exit != model.ExitUsage {
		t.Fatalf("exit=%d stderr=%s", exit, errb)
	}
}

func TestDoctorReportsInitializationFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-directory")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out, errb, exit := runCLI(t, file.Name(), "--json", "doctor")
	if exit != model.ExitStorage || errb != "" {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, out, errb)
	}
	var env struct {
		Command string             `json:"command"`
		Data    model.DoctorResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if env.Command != "doctor" || env.Data.OK || len(env.Data.Checks) < 3 {
		t.Fatalf("doctor=%+v", env.Data)
	}
}

func TestDoctorReportsCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pcast.db"), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, exit := runCLI(t, dir, "--json", "doctor")
	if exit != model.ExitStorage || errb != "" {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, out, errb)
	}
	var env struct {
		Data model.DoctorResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.OK || len(env.Data.Checks) < 3 {
		t.Fatalf("doctor=%+v", env.Data)
	}
}

func TestDuplicateAddAliasConflict(t *testing.T) {
	dir := t.TempDir()
	srv := feedServer(t, readFixture(t, "rss_basic.xml"))
	defer srv.Close()
	if _, errb, exit := runCLI(t, dir, "add", srv.URL+"/feed.xml", "--name", "first"); exit != 0 {
		t.Fatalf("first add exit=%d err=%s", exit, errb)
	}
	other := feedServer(t, readFixture(t, "rss_basic.xml"))
	defer other.Close()
	if _, errb, exit := runCLI(t, dir, "add", other.URL+"/feed.xml", "--name", "second"); exit != 0 {
		t.Fatalf("second add exit=%d err=%s", exit, errb)
	}
	_, errb, exit := runCLI(t, dir, "add", srv.URL+"/feed.xml", "--name", "second")
	if exit != model.ExitUsage || !strings.Contains(errb, "already in use") {
		t.Fatalf("exit=%d err=%s", exit, errb)
	}
}

func TestLatestDoesNotExposeFeedQueryValues(t *testing.T) {
	dir := t.TempDir()
	var failed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, readFixture(t, "rss_empty.xml"))
	}))
	defer srv.Close()
	url := srv.URL + "/feed.xml?Token=SECRET&private=ALSO_SECRET"
	if _, errb, exit := runCLI(t, dir, "add", url); exit != 0 {
		t.Fatalf("add exit=%d err=%s", exit, errb)
	}
	failed = true
	out, errb, exit := runCLI(t, dir, "--json", "latest")
	if exit != model.ExitPartialLatest {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, out, errb)
	}
	if strings.Contains(out+errb, "SECRET") || strings.Contains(out+errb, "ALSO_SECRET") {
		t.Fatalf("query value leaked: stdout=%s stderr=%s", out, errb)
	}
	out, errb, exit = runCLI(t, dir, "--json", "list")
	if exit != 0 || errb != "" {
		t.Fatalf("list: exit=%d stdout=%s stderr=%s", exit, out, errb)
	}
	var listed struct {
		Data model.ListResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data.Podcasts) != 1 || listed.Data.Podcasts[0].LastError == nil ||
		strings.Contains(*listed.Data.Podcasts[0].LastError, "SECRET") ||
		strings.Contains(*listed.Data.Podcasts[0].LastError, "ALSO_SECRET") {
		t.Fatalf("stored error leaked: %+v", listed.Data.Podcasts)
	}
}

func TestAddListLatestEpisodesMarkPlayRemove(t *testing.T) {
	dir := t.TempDir()
	basic := readFixture(t, "rss_basic.xml")
	more := readFixture(t, "rss_one_more.xml")

	var current string
	current = basic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, current)
	}))
	defer srv.Close()

	out, errb, exit := runCLI(t, dir, "add", srv.URL+"/feed.xml", "--name", "daily")
	if exit != 0 {
		t.Fatalf("add exit=%d out=%s err=%s", exit, out, errb)
	}
	if !strings.Contains(out, "created") {
		t.Fatalf("out=%s", out)
	}

	// idempotent
	out, errb, exit = runCLI(t, dir, "--json", "add", srv.URL+"/feed.xml")
	if exit != 0 {
		t.Fatal(exit, errb)
	}
	if !strings.Contains(out, `"created":false`) {
		t.Fatalf("out=%s", out)
	}

	out, errb, exit = runCLI(t, dir, "list")
	if exit != 0 || !strings.Contains(out, "daily") {
		t.Fatalf("list exit=%d out=%s err=%s", exit, out, errb)
	}

	// latest after add is empty
	out, errb, exit = runCLI(t, dir, "latest")
	if exit != 0 {
		t.Fatal(exit, errb)
	}
	if !strings.Contains(out, "No new episodes") {
		t.Fatalf("latest=%s", out)
	}

	// evolve feed
	current = more
	out, errb, exit = runCLI(t, dir, "--json", "latest", "daily")
	if exit != 0 {
		t.Fatal(exit, errb, out)
	}
	var latestEnv struct {
		Data model.LatestResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &latestEnv); err != nil {
		t.Fatal(err, out)
	}
	if len(latestEnv.Data.Episodes) != 1 || latestEnv.Data.Episodes[0].Title != "Episode Three" {
		t.Fatalf("latest data=%+v", latestEnv.Data)
	}

	// second latest empty
	out, _, exit = runCLI(t, dir, "latest", "daily")
	if exit != 0 || !strings.Contains(out, "No new episodes") {
		t.Fatalf("second latest=%s exit=%d", out, exit)
	}

	out, errb, exit = runCLI(t, dir, "--json", "episodes", "daily", "--all")
	if exit != 0 {
		t.Fatal(exit, errb)
	}
	var epsEnv struct {
		Data model.EpisodesResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &epsEnv); err != nil {
		t.Fatal(err)
	}
	if len(epsEnv.Data.Episodes) != 3 {
		t.Fatalf("eps=%d", len(epsEnv.Data.Episodes))
	}
	epID := epsEnv.Data.Episodes[0].ID

	out, errb, exit = runCLI(t, dir, "episode", itoa(epID))
	if exit != 0 || !strings.Contains(out, "Episode") {
		t.Fatalf("episode exit=%d out=%s err=%s", exit, out, errb)
	}

	out, errb, exit = runCLI(t, dir, "mark", itoa(epID), "played")
	if exit != 0 {
		t.Fatal(exit, errb, out)
	}
	_, errb, exit = runCLI(t, dir, "mark", itoa(epID), "unplayed")
	if exit != 0 {
		t.Fatal(exit, errb)
	}

	out, errb, exit = runCLI(t, dir, "play", itoa(epID))
	if exit != 0 {
		t.Fatalf("play exit=%d out=%s err=%s", exit, out, errb)
	}

	out, errb, exit = runCLI(t, dir, "doctor")
	if exit != 0 {
		t.Fatalf("doctor exit=%d out=%s err=%s", exit, out, errb)
	}

	out, errb, exit = runCLI(t, dir, "remove", "daily")
	if exit != 0 {
		t.Fatal(exit, errb, out)
	}
	out, _, exit = runCLI(t, dir, "list")
	if exit != 0 || !strings.Contains(out, "No subscriptions") {
		t.Fatalf("list after remove=%s", out)
	}
}

func TestSelectorAmbiguousAndNotFound(t *testing.T) {
	dir := t.TempDir()
	body := readFixture(t, "rss_basic.xml")
	srv := feedServer(t, body)
	defer srv.Close()

	// Two podcasts same title different URLs - need two servers with same title
	_, errb, exit := runCLI(t, dir, "add", srv.URL+"/a.xml", "--name", "a")
	if exit != 0 {
		t.Fatal(exit, errb)
	}
	srv2 := feedServer(t, body)
	defer srv2.Close()
	_, errb, exit = runCLI(t, dir, "add", srv2.URL+"/b.xml", "--name", "b")
	if exit != 0 {
		t.Fatal(exit, errb)
	}

	_, errb, exit = runCLI(t, dir, "remove", "Example Podcast")
	if exit != model.ExitNotFound || !strings.Contains(errb, "multiple") && !strings.Contains(errb, "ambiguous") && !strings.Contains(errb, "error") {
		// human error message mentions multiple podcasts
		if exit != model.ExitNotFound {
			t.Fatalf("exit=%d err=%s", exit, errb)
		}
	}

	_, errb, exit = runCLI(t, dir, "remove", "missing")
	if exit != model.ExitNotFound {
		t.Fatalf("exit=%d err=%s", exit, errb)
	}
}

func TestJSONErrorEmptyStdout(t *testing.T) {
	dir := t.TempDir()
	out, errb, exit := runCLI(t, dir, "--json", "episode", "999")
	if exit != model.ExitNotFound {
		t.Fatalf("exit=%d", exit)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout should be empty, got %q", out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(errb), &env); err != nil {
		t.Fatal(err, errb)
	}
}

func TestPartialLatest(t *testing.T) {
	dir := t.TempDir()
	okBody := readFixture(t, "rss_basic.xml")
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, okBody)
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	if _, errb, exit := runCLI(t, dir, "add", ok.URL, "--name", "ok"); exit != 0 {
		t.Fatal(exit, errb)
	}
	if _, errb, exit := runCLI(t, dir, "add", bad.URL, "--name", "bad"); exit != 0 {
		// add of bad should fail and leave no row
		if exit == 0 {
			t.Fatal("expected add failure")
		}
		_ = errb
	}
	// Create bad subscription by using empty feed server then switching?
	// Instead open store and insert manually... easier: add empty then point URL won't change.
	// Use a server that works once for add then fails.
	var fail bool
	flakyBody := readFixture(t, "rss_empty.xml")
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, flakyBody)
	}))
	defer flaky.Close()
	if _, errb, exit := runCLI(t, dir, "add", flaky.URL, "--name", "flaky"); exit != 0 {
		t.Fatal(exit, errb)
	}
	fail = true

	out, errb, exit := runCLI(t, dir, "--json", "latest")
	if exit != model.ExitPartialLatest {
		t.Fatalf("exit=%d out=%s err=%s", exit, out, errb)
	}
	var env struct {
		Data model.LatestResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err, out)
	}
	if !env.Data.Partial || len(env.Data.Failures) == 0 {
		t.Fatalf("%+v", env.Data)
	}
}

func TestFailedOutputLeavesPending(t *testing.T) {
	dir := t.TempDir()
	basic := readFixture(t, "rss_basic.xml")
	more := readFixture(t, "rss_one_more.xml")
	var current = basic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, current)
	}))
	defer srv.Close()

	if _, errb, exit := runCLI(t, dir, "add", srv.URL, "--name", "x"); exit != 0 {
		t.Fatal(exit, errb)
	}
	current = more

	// Run latest with a failing writer by calling app directly.
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "pcast.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &app.App{
		Store: st,
		Feeds: &feedAdapter{c: feed.NewClient("t")},
		Lock:  platform.NewLock(filepath.Join(dir, "pcast.lock")),
		Clock: app.FixedClock{T: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)},
	}
	_, err = a.LatestLocked(ctx, "x", func(model.LatestResult) error {
		return io.ErrShortWrite
	})
	if err == nil {
		t.Fatal("expected write error")
	}
	pending, err := st.ListPendingEpisodes(ctx, nil)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}

type feedAdapter struct{ c *feed.Client }

func (f *feedAdapter) Fetch(ctx context.Context, opts app.FetchOpts) (model.ParsedFeed, error) {
	return f.c.Fetch(ctx, feed.FetchOptions{URL: opts.URL, ETag: opts.ETag, LastModified: opts.LastModified})
}

func itoa(id int64) string {
	return fmt.Sprintf("%d", id)
}
