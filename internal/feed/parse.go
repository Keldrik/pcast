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
	// Relative enclosures are relative to the final feed response URL, not
	// channel <link>, which normally points at the show's homepage.
	base := resolved
	if base == "" {
		base = submitted
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
		ep, ok := mapItem(item, base)
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

func mapItem(item *gofeed.Item, baseURL string) (model.ParsedEpisode, bool) {
	encURL, mediaType, mediaLen := selectEnclosure(item, baseURL)
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

func selectEnclosure(item *gofeed.Item, baseURL string) (encURL string, mediaType *string, mediaLen *int64) {
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
		u := resolveMediaURL(strings.TrimSpace(e.URL), baseURL)
		if u == "" {
			continue
		}
		typ := strings.TrimSpace(strings.ToLower(strings.SplitN(e.Type, ";", 2)[0]))
		var length int64
		if e.Length != "" {
			if n, err := strconv.ParseInt(e.Length, 10, 64); err == nil {
				length = n
			}
		}
		audioURL := hasAudioExtension(u)
		if !strings.HasPrefix(typ, "audio/") && !audioURL && hasNonAudioExtension(u) {
			continue
		}
		score := 0
		switch {
		case strings.HasPrefix(typ, "audio/"):
			score = 100
		case typ == "" || typ == "application/octet-stream":
			score = 50
		case audioURL:
			// Some feeds label audio as application/* or text/*. Keep it only
			// when the URL provides independent audio evidence.
			score = 40
		default:
			continue
		}
		if audioURL {
			score += 5
		}
		cands = append(cands, cand{url: u, typ: typ, len: length, score: score})
	}
	// Also consider media extensions on link when no enclosure.
	if len(cands) == 0 && item.Link != "" {
		if norm := resolveMediaURL(item.Link, baseURL); norm != "" && hasAudioExtension(norm) {
			cands = append(cands, cand{url: norm, score: 40})
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
	// Omit the entire query. Feed URLs are user-controlled and a private
	// parameter can use any name, casing, or malformed encoding.
	if u.RawQuery != "" {
		u.RawQuery = "REDACTED"
	}
	u.Fragment = ""
	return u.String()
}

// FormatBytes is a tiny helper for error messages.
func FormatBytes(n int64) string {
	return fmt.Sprintf("%d bytes", n)
}

// resolveMediaURL normalizes absolute http(s) URLs or resolves relative ones against baseURL.
func resolveMediaURL(raw, baseURL string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if norm, err := NormalizeURL(raw); err == nil {
		return norm
	}
	if baseURL == "" {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref).String()
	norm, err := NormalizeURL(abs)
	if err != nil {
		return ""
	}
	return norm
}

func hasNonAudioExtension(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	for _, ext := range []string{".html", ".htm", ".xml", ".json", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".mp4", ".webm"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func hasAudioExtension(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	for _, ext := range []string{".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}
