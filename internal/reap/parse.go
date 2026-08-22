// Package reap selects long-lived, dormant, expensive processes for
// termination. It is deliberately agnostic about what any process *is*: the
// decision rests on measurable properties (footprint, age, duty cycle) plus
// user-configured probes that ask a process directly whether it is safe to
// kill. Nothing here hardcodes knowledge of editors, language servers, or any
// other program.
package reap

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration extends time.ParseDuration with a day unit, because reap
// thresholds are naturally expressed in days ("--older 2d") and the stdlib
// stops at hours.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (try 30m, 12h, 2d)", s)
	}
	return d, nil
}

// ParseSize converts a human size ("200M", "1.5G", "512K", "2048") to KiB.
// A bare number is taken as MiB, which is the unit people mean when they type
// "--min-mem 500" and avoids a silent thousand-fold mistake either way.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := float64(1024) // default: MiB -> KiB
	switch {
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "KB"):
		mult, s = 1, strings.TrimSuffix(strings.TrimSuffix(s, "B"), "K")
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "MB"):
		mult, s = 1024, strings.TrimSuffix(strings.TrimSuffix(s, "B"), "M")
	case strings.HasSuffix(s, "G"), strings.HasSuffix(s, "GB"):
		mult, s = 1024*1024, strings.TrimSuffix(strings.TrimSuffix(s, "B"), "G")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q (try 500M, 2G)", s)
	}
	return int64(v * mult), nil
}

// FormatDuration renders an age compactly ("2d0h", "3h12m", "45s") for table
// columns where a full time.Duration string is too wide.
func FormatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
