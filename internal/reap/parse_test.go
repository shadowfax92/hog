package reap

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"12h", 12 * time.Hour},
		{"2d", 48 * time.Hour},
		{"1.5d", 36 * time.Hour},
		{"90s", 90 * time.Second},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "2w", "abc", "12"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) should have failed", bad)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"512K", 512},
		{"200M", 200 * 1024},
		{"1.5G", 1536 * 1024},
		{"2G", 2 * 1024 * 1024},
		{"500", 500 * 1024}, // bare number means MiB
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if err != nil {
			t.Errorf("ParseSize(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSize(%q) = %d KiB, want %d", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "big", "-5M"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should have failed", bad)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{48*time.Hour + 3*time.Hour, "2d3h"},
		{3*time.Hour + 12*time.Minute, "3h12m"},
		{45 * time.Minute, "45m"},
		{20 * time.Second, "20s"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.in); got != c.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
