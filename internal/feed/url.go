package feed

import (
	"net/url"
	"strings"

	"github.com/Keldrik/pcast/internal/model"
)

// NormalizeURL validates and normalizes an HTTP(S) feed or enclosure URL.
// It lowercases scheme and host, strips default ports and fragments, and rejects
// credentials. Path and query are preserved (aside from empty path becoming "/").
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", model.InvalidArgument("URL must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", model.InvalidArgumentf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", model.InvalidArgumentf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", model.InvalidArgument("URL must include a host")
	}
	if u.User != nil {
		return "", model.InvalidArgument("URL must not contain embedded credentials")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	switch {
	case port == "80" && u.Scheme == "http":
		port = ""
	case port == "443" && u.Scheme == "https":
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	if u.Path == "" {
		u.Path = "/"
	}
	// Drop fragment; not meaningful for feed identity.
	u.Fragment = ""
	// Reject weirdness like javascript via opaque.
	if u.Opaque != "" {
		return "", model.InvalidArgument("opaque URLs are not supported")
	}
	return u.String(), nil
}

// IsHTTPURL reports whether s looks like an http(s) URL with a host.
func IsHTTPURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
