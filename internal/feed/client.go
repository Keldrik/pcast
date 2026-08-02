package feed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/Keldrik/pcast/internal/model"
)

const (
	// DefaultTimeout is the per-request timeout.
	DefaultTimeout = 30 * time.Second
	// MaxRedirects is the maximum redirect hops.
	MaxRedirects = 5
	// MaxBodyBytes limits decompressed feed body size (~10 MiB).
	MaxBodyBytes = 10 << 20
	// DefaultConcurrency is the worker count for multi-feed checks.
	DefaultConcurrency = 4
)

// Client fetches and parses podcast feeds.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	Parser    *gofeed.Parser
}

// NewClient builds a feed client with documented HTTP policy.
func NewClient(userAgent string) *Client {
	if userAgent == "" {
		userAgent = "pcast/dev"
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	c := &Client{
		UserAgent: userAgent,
		Parser:    gofeed.NewParser(),
	}
	c.HTTP = &http.Client{
		Timeout:   DefaultTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", MaxRedirects)
			}
			// Reject credentialed redirect targets.
			if req.URL.User != nil {
				return fmt.Errorf("redirect URL must not contain credentials")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect scheme not allowed")
			}
			return nil
		},
	}
	return c
}

// FetchOptions controls a single feed fetch.
type FetchOptions struct {
	URL          string
	ETag         *string
	LastModified *string
}

// Fetch retrieves and parses a feed URL.
func (c *Client) Fetch(ctx context.Context, opts FetchOptions) (model.ParsedFeed, error) {
	submitted, err := NormalizeURL(opts.URL)
	if err != nil {
		return model.ParsedFeed{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, submitted, nil)
	if err != nil {
		return model.ParsedFeed{}, model.FeedUnavailable("build request", err)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	if opts.ETag != nil && *opts.ETag != "" {
		req.Header.Set("If-None-Match", *opts.ETag)
	}
	if opts.LastModified != nil && *opts.LastModified != "" {
		req.Header.Set("If-Modified-Since", *opts.LastModified)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return model.ParsedFeed{}, model.FeedUnavailable("request cancelled or timed out", err)
		}
		return model.ParsedFeed{}, model.FeedUnavailable("request failed", err)
	}
	defer resp.Body.Close()

	resolved := submitted
	if resp.Request != nil && resp.Request.URL != nil {
		if r, err := NormalizeURL(resp.Request.URL.String()); err == nil {
			resolved = r
		} else {
			resolved = resp.Request.URL.String()
		}
	}

	etag := headerPtr(resp.Header, "ETag")
	lastMod := headerPtr(resp.Header, "Last-Modified")

	if resp.StatusCode == http.StatusNotModified {
		return model.ParsedFeed{
			SubmittedURL: submitted,
			ResolvedURL:  resolved,
			ETag:         firstNonNil(etag, opts.ETag),
			LastModified: firstNonNil(lastMod, opts.LastModified),
			HTTPStatus:   resp.StatusCode,
			NotModified:  true,
			Episodes:     []model.ParsedEpisode{},
		}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain limited body for cleanliness.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return model.ParsedFeed{
				SubmittedURL: submitted,
				ResolvedURL:  resolved,
				HTTPStatus:   resp.StatusCode,
			}, model.FeedUnavailable(
				fmt.Sprintf("unexpected HTTP status %d from %s", resp.StatusCode, RedactURL(resolved)),
				nil,
			)
	}

	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return model.ParsedFeed{
			SubmittedURL: submitted,
			ResolvedURL:  resolved,
			HTTPStatus:   resp.StatusCode,
		}, model.FeedUnavailable("read body", err)
	}
	if len(body) > MaxBodyBytes {
		return model.ParsedFeed{
			SubmittedURL: submitted,
			ResolvedURL:  resolved,
			HTTPStatus:   resp.StatusCode,
		}, model.FeedUnavailable(fmt.Sprintf("feed body exceeds %s", FormatBytes(MaxBodyBytes)), nil)
	}

	gf, err := c.Parser.ParseString(string(body))
	if err != nil {
		return model.ParsedFeed{
			SubmittedURL: submitted,
			ResolvedURL:  resolved,
			HTTPStatus:   resp.StatusCode,
		}, model.InvalidFeed("parse feed", err)
	}

	parsed, err := MapFeed(gf, submitted, resolved, resp.StatusCode, etag, lastMod)
	if err != nil {
		return model.ParsedFeed{
			SubmittedURL: submitted,
			ResolvedURL:  resolved,
			HTTPStatus:   resp.StatusCode,
		}, err
	}
	return parsed, nil
}

func headerPtr(h http.Header, key string) *string {
	v := strings.TrimSpace(h.Get(key))
	if v == "" {
		return nil
	}
	return &v
}

func firstNonNil(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}
