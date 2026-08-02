package model

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeAndCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		exit int
		code string
	}{
		{nil, ExitOK, ""},
		{InvalidArgument("bad"), ExitUsage, CodeInvalidArgument},
		{NotFound("missing"), ExitNotFound, CodeNotFound},
		{AmbiguousSelector("many", []int64{1, 2}), ExitNotFound, CodeAmbiguousSelector},
		{FeedUnavailable("net", errors.New("timeout")), ExitNetwork, CodeFeedUnavailable},
		{InvalidFeed("xml", errors.New("parse")), ExitNetwork, CodeInvalidFeed},
		{Storage("db", errors.New("disk")), ExitStorage, CodeStorageError},
		{LockUnavailable("lock", errors.New("busy")), ExitStorage, CodeLockUnavailable},
		{PlayerUnavailable("no player"), ExitPlayer, CodePlayerUnavailable},
		{PlayerFailed("boom", errors.New("exit 1")), ExitPlayer, CodePlayerFailed},
		{Internal("oops", errors.New("x")), ExitInternal, CodeInternalError},
		{errors.New("plain"), ExitInternal, CodeInternalError},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.exit {
			t.Errorf("ExitCode(%v)=%d want %d", tc.err, got, tc.exit)
		}
		if got := CodeOf(tc.err); got != tc.code {
			t.Errorf("CodeOf(%v)=%q want %q", tc.err, got, tc.code)
		}
	}
}

func TestWrapPreservesTypedCode(t *testing.T) {
	base := NotFound("episode 9")
	wrapped := Wrap(base, "lookup failed")
	if CodeOf(wrapped) != CodeNotFound {
		t.Fatalf("code=%s", CodeOf(wrapped))
	}
	if ExitCode(wrapped) != ExitNotFound {
		t.Fatalf("exit=%d", ExitCode(wrapped))
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("expected errors.Is to find base")
	}
}

func TestWrapPlainBecomesInternal(t *testing.T) {
	wrapped := Wrap(fmt.Errorf("disk full"), "save failed")
	if CodeOf(wrapped) != CodeInternalError {
		t.Fatalf("code=%s", CodeOf(wrapped))
	}
}

func TestAmbiguousSelectorDetails(t *testing.T) {
	err := AmbiguousSelector("many titles", []int64{3, 7})
	d := DetailsOf(err)
	cands, ok := d["candidates"].([]any)
	if !ok || len(cands) != 2 {
		t.Fatalf("details=%v", d)
	}
}

func TestEpisodeFilterValidate(t *testing.T) {
	if err := (EpisodeFilter{All: true, Limit: 5}).Validate(); err == nil {
		t.Fatal("expected error for --all with limit")
	}
	if err := (EpisodeFilter{Limit: -1}).Validate(); err == nil {
		t.Fatal("expected error for negative limit")
	}
	if err := (EpisodeFilter{All: true}).Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := (EpisodeFilter{}).EffectiveLimit(); got != 20 {
		t.Fatalf("default limit=%d", got)
	}
	if got := (EpisodeFilter{All: true}).EffectiveLimit(); got != 0 {
		t.Fatalf("all limit=%d", got)
	}
}
