package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// IdentityKey builds a deterministic episode identity following project precedence:
// 1. non-empty GUID
// 2. normalized enclosure URL
// 3. hash of stable available fields
func IdentityKey(guid *string, enclosureURL, title string, published *time.Time, mediaType *string, mediaLength *int64) string {
	if guid != nil {
		g := strings.TrimSpace(*guid)
		if g != "" {
			return "guid:" + g
		}
	}
	if enc := strings.TrimSpace(enclosureURL); enc != "" {
		if norm, err := NormalizeURL(enc); err == nil {
			return "enc:" + norm
		}
		return "enc:" + enc
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "title=%s\n", title)
	if published != nil {
		_, _ = fmt.Fprintf(h, "pub=%s\n", published.UTC().Format(time.RFC3339Nano))
	}
	if mediaType != nil {
		_, _ = fmt.Fprintf(h, "type=%s\n", *mediaType)
	}
	if mediaLength != nil {
		_, _ = fmt.Fprintf(h, "len=%d\n", *mediaLength)
	}
	return "hash:" + hex.EncodeToString(h.Sum(nil))
}
