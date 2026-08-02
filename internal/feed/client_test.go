package feed_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/feed"
	"github.com/Keldrik/pcast/internal/model"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchRSSAndAtom(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rss.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
		_, _ = w.Write([]byte(fixture(t, "rss_basic.xml")))
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture(t, "atom_basic.xml")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := feed.NewClient("pcast-test")
	ctx := context.Background()

	rss, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/rss.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if rss.Title != "Example Podcast" || len(rss.Episodes) != 2 {
		t.Fatalf("rss=%+v", rss)
	}
	if rss.ETag == nil || *rss.ETag != `"v1"` {
		t.Fatalf("etag=%v", rss.ETag)
	}
	if rss.Episodes[0].DurationSeconds == nil && rss.Episodes[1].DurationSeconds == nil {
		t.Fatal("expected duration parse")
	}

	atom, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/atom.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if atom.Title != "Atom Show" || len(atom.Episodes) != 1 {
		t.Fatalf("atom=%+v", atom)
	}
}

func TestFetch304AndRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/final.xml", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(fixture(t, "rss_empty.xml")))
	})
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final.xml", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := feed.NewClient("pcast-test")
	ctx := context.Background()

	first, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/redir"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(first.ResolvedURL, "/final.xml") {
		t.Fatalf("resolved=%s", first.ResolvedURL)
	}
	if first.Title != "Empty Feed" || len(first.Episodes) != 0 {
		t.Fatalf("%+v", first)
	}

	etag := `"abc"`
	nm, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/final.xml", ETag: &etag})
	if err != nil {
		t.Fatal(err)
	}
	if !nm.NotModified || nm.HTTPStatus != 304 {
		t.Fatalf("%+v", nm)
	}
}

func TestFetchErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bad", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture(t, "malformed.xml")))
	})
	mux.HandleFunc("/500", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Write more than limit
		chunk := make([]byte, 1024)
		for i := 0; i < (feed.MaxBodyBytes/1024)+2; i++ {
			_, _ = w.Write(chunk)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := feed.NewClient("pcast-test")
	ctx := context.Background()

	_, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/bad"})
	if err == nil || model.CodeOf(err) != model.CodeInvalidFeed {
		t.Fatalf("malformed: %v code=%s", err, model.CodeOf(err))
	}
	_, err = c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/500"})
	if err == nil || model.CodeOf(err) != model.CodeFeedUnavailable {
		t.Fatalf("500: %v", err)
	}
	_, err = c.Fetch(ctx, feed.FetchOptions{URL: srv.URL + "/big"})
	if err == nil {
		t.Fatal("expected oversized body error")
	}
}

func TestFetchCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	c := feed.NewClient("pcast-test")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Fetch(ctx, feed.FetchOptions{URL: srv.URL})
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestRelativeEnclosureResolved(t *testing.T) {
	body := `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>Rel</title>
<link>https://media.example/show/</link>
<item>
<title>One</title>
<enclosure url="ep/one.mp3" type="audio/mpeg" length="1"/>
</item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := feed.NewClient("pcast-test")
	parsed, err := c.Fetch(context.Background(), feed.FetchOptions{URL: srv.URL + "/feed.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Episodes) != 1 {
		t.Fatalf("episodes=%v", parsed.Episodes)
	}
	want := srv.URL + "/ep/one.mp3"
	if parsed.Episodes[0].EnclosureURL != want {
		t.Fatalf("enclosure=%q want %q", parsed.Episodes[0].EnclosureURL, want)
	}
}

func TestRejectsNonAudioEnclosures(t *testing.T) {
	body := `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>Media</title><link>https://example.test/show/</link>
<item><title>Video</title><enclosure url="video.mp4" type="video/mp4"/></item>
<item><title>HTML</title><enclosure url="page.html" type="text/html"/></item>
<item><title>Audio</title><enclosure url="audio.mp3" type="audio/mpeg"/></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	parsed, err := feed.NewClient("pcast-test").Fetch(context.Background(), feed.FetchOptions{URL: srv.URL + "/feed.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Episodes) != 1 || parsed.Episodes[0].Title != "Audio" {
		t.Fatalf("episodes=%+v", parsed.Episodes)
	}
}

func TestIdentityStable(t *testing.T) {
	g := "abc"
	k1 := feed.IdentityKey(&g, "https://cdn/x.mp3", "T", nil, nil, nil)
	k2 := feed.IdentityKey(&g, "https://cdn/other.mp3", "Other", nil, nil, nil)
	if k1 != k2 || !strings.HasPrefix(k1, "guid:") {
		t.Fatalf("%s %s", k1, k2)
	}
	k3 := feed.IdentityKey(nil, "https://CDN.Example.com/x.mp3", "T", nil, nil, nil)
	k4 := feed.IdentityKey(nil, "https://cdn.example.com/x.mp3", "T", nil, nil, nil)
	if k3 != k4 {
		t.Fatalf("enc identity %s vs %s", k3, k4)
	}
}
