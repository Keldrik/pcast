package feed

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/Keldrik/pcast/internal/model"
)

var durationRe = regexp.MustCompile(`(?i)^\s*(?:(\d+):)?(\d{1,2}):(\d{2})\s*$`)

// MapFeed converts a gofeed.Feed into domain types.
func MapFeed(gf *gofeed.Feed, submitted, resolved string, httpStatus int, etag, lastMod *string) (model.ParsedFeed, error) {
	if gf == nil {
		return model.ParsedFeed{}, model.InvalidFeed("empty feed", nil)
	}
	title := strings.TrimSpace(gf.Title)
	if title == "" {
		return model.ParsedFeed{}, model.InvalidFeed("feed title is required", nil)
	}
	out := model.ParsedFeed{
		SubmittedURL: submitted,
		ResolvedURL:  resolved,
		Title:        title,
		Author:       firstNonEmpty(authorOf(gf), nil),
		Description:  strPtrTrim(gf.Description),
		ETag:         etag,
		LastModified: lastMod,
		HTTPStatus:   httpStatus,
		Episodes:     nil,
	}
	for _, item := range gf.Items {
		if item == nil {
			continue
		}
		ep, ok := mapItem(item)
		if !ok {
			continue
		}
		out.Episodes = append(out.Episodes, ep)
	}
	if out.Episodes == nil {
		out.Episodes = []model.ParsedEpisode{}
	}
	return out, nil
}

func mapItem(item *gofeed.Item) (model.ParsedEpisode, bool) {
	encURL, mediaType, mediaLen := selectEnclosure(item)
	if encURL == "" {
		return model.ParsedEpisode{}, false
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = encURL
	}
	var guid *string
	if g := strings.TrimSpace(item.GUID); g != "" {
		guid = &g
	}
	var published *time.Time
	if item.PublishedParsed != nil {
		t := item.PublishedParsed.UTC()
		published = &t
	} else if item.UpdatedParsed != nil {
		t := item.UpdatedParsed.UTC()
		published = &t
	}
	dur := parseDurationSeconds(item)
	desc := strPtrTrim(item.Description)
	if desc == nil {
		desc = strPtrTrim(item.Content)
	}
	idKey := IdentityKey(guid, encURL, title, published, mediaType, mediaLen)
	return model.ParsedEpisode{
		GUID:            guid,
		Title:           title,
		Description:     desc,
		PublishedAt:     published,
		DurationSeconds: dur,
		EnclosureURL:    encURL,
		MediaType:       mediaType,
		MediaLength:     mediaLen,
		IdentityKey:     idKey,
	}, true
}

func selectEnclosure(item *gofeed.Item) (encURL string, mediaType *string, mediaLen *int64) {
	type cand struct {
		url   string
		typ   string
		len   int64
		score int
	}
	var cands []cand
	for _, e := range item.Enclosures {
		if e == nil {
			continue
		}
		u := strings.TrimSpace(e.URL)
		if u == "" {
			continue
		}
		// Resolve relative URLs are uncommon; require absolute http(s).
		if norm, err := NormalizeURL(u); err == nil {
			u = norm
		} else if !IsHTTPURL(u) {
			continue
		}
		typ := strings.TrimSpace(strings.ToLower(e.Type))
		var length int64
		if e.Length != "" {
			if n, err := strconv.ParseInt(e.Length, 10, 64); err == nil {
				length = n
			}
		}
		score := 0
		switch {
		case strings.HasPrefix(typ, "audio/"):
			score = 100
		case typ == "" || typ == "application/octet-stream":
			score = 50
		case strings.HasPrefix(typ, "video/"):
			score = 20
		default:
			score = 10
		}
		// Prefer known audio extensions slightly.
		lower := strings.ToLower(u)
		for _, ext := range []string{".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav"} {
			if strings.Contains(lower, ext) {
				score += 5
				break
			}
		}
		cands = append(cands, cand{url: u, typ: typ, len: length, score: score})
	}
	// Also consider media extensions on link when no enclosure.
	if len(cands) == 0 && item.Link != "" {
		if norm, err := NormalizeURL(item.Link); err == nil {
			lower := strings.ToLower(norm)
			for _, ext := range []string{".mp3", ".m4a", ".aac", ".ogg", ".opus"} {
				if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") {
					cands = append(cands, cand{url: norm, score: 40})
					break
				}
			}
		}
	}
	if len(cands) == 0 {
		return "", nil, nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.score > best.score {
			best = c
		}
	}
	var mt *string
	if best.typ != "" {
		mt = &best.typ
	}
	var ml *int64
	if best.len > 0 {
		ml = &best.len
	}
	return best.url, mt, ml
}

// ParseDurationSeconds parses common podcast duration formats into seconds.
func ParseDurationSeconds(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Plain integer seconds.
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return &n
	}
	// HH:MM:SS or MM:SS
	if m := durationRe.FindStringSubmatch(s); m != nil {
		var h, min, sec int
		if m[1] != "" {
			h, _ = strconv.Atoi(m[1])
		}
		min, _ = strconv.Atoi(m[2])
		sec, _ = strconv.Atoi(m[3])
		total := h*3600 + min*60 + sec
		return &total
	}
	return nil
}

func parseDurationSeconds(item *gofeed.Item) *int {
	// gofeed puts itunes:duration into Custom / ITunesExt.
	if item.ITunesExt != nil && item.ITunesExt.Duration != "" {
		if d := ParseDurationSeconds(item.ITunesExt.Duration); d != nil {
			return d
		}
	}
	if item.Extensions != nil {
		if itunes, ok := item.Extensions["itunes"]; ok {
			if durs, ok := itunes["duration"]; ok && len(durs) > 0 {
				if d := ParseDurationSeconds(durs[0].Value); d != nil {
					return d
				}
			}
		}
	}
	return nil
}

func authorOf(gf *gofeed.Feed) *string {
	if gf.Author != nil && strings.TrimSpace(gf.Author.Name) != "" {
		return strPtrTrim(gf.Author.Name)
	}
	if gf.ITunesExt != nil && strings.TrimSpace(gf.ITunesExt.Author) != "" {
		return strPtrTrim(gf.ITunesExt.Author)
	}
	return nil
}

func strPtrTrim(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func firstNonEmpty(vals ...*string) *string {
	for _, v := range vals {
		if v != nil && *v != "" {
			return v
		}
	}
	return nil
}

// HostOf returns the URL host for diagnostics (no userinfo).
func HostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// RedactURL strips query values that might be sensitive for diagnostics.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = nil
	}
	// Keep keys but blank values for common secret-ish params.
	q := u.Query()
	changed := false
	for _, k := range []string{"token", "key", "api_key", "apikey", "auth", "password", "secret", "access_token"} {
		if q.Has(k) {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// FormatBytes is a tiny helper for error messages.
func FormatBytes(n int64) string {
	return fmt.Sprintf("%d bytes", n)
}
