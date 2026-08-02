package feed

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://Example.COM/Feed.xml", "https://example.com/Feed.xml", false},
		{"http://example.com:80/a", "http://example.com/a", false},
		{"https://example.com:443/a", "https://example.com/a", false},
		{"https://example.com", "https://example.com/", false},
		{"https://example.com/a#frag", "https://example.com/a", false},
		{"https://example.com/a?x=1&y=2", "https://example.com/a?x=1&y=2", false},
		{"ftp://example.com/a", "", true},
		{"https:///nohost", "", true},
		{"https://user:pass@example.com/a", "", true},
		{"", "", true},
		{"not a url", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeURL(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeURL(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want *int
	}{
		{"3600", intPtr(3600)},
		{"1:02:03", intPtr(3723)},
		{"12:03", intPtr(723)},
		{"", nil},
		{"nope", nil},
	}
	for _, tc := range cases {
		got := ParseDurationSeconds(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("%q got %v", tc.in, *got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			t.Errorf("%q got %v want %d", tc.in, got, *tc.want)
		}
	}
}

func intPtr(n int) *int { return &n }
