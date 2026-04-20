package language

import (
	"testing"
	"time"
)

func TestParseExtendedDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"0.5d", 12 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseExtendedDuration(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %s want %s", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "abc", "1x"} {
		if _, err := parseExtendedDuration(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}
