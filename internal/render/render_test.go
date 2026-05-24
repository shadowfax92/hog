package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLevelOfThresholds(t *testing.T) {
	cases := []struct {
		frac float64
		want Level
	}{
		{0.0, Low},
		{0.09, Low},
		{0.10, Med},
		{0.30, Med},
		{0.3001, High},
		{0.9, High},
	}
	for _, c := range cases {
		if got := LevelOf(c.frac); got != c.want {
			t.Errorf("LevelOf(%v) = %v, want %v", c.frac, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		kib  int64
		want string
	}{
		{512, "512K"},
		{348160, "340M"},  // 340 MiB
		{1258291, "1.2G"}, // ~1.2 GiB
	}
	for _, c := range cases {
		if got := HumanBytes(c.kib); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.kib, got, c.want)
		}
	}
}

func TestTruncateMiddle(t *testing.T) {
	if got := TruncateMiddle("abcdefghij", 20); got != "abcdefghij" {
		t.Errorf("short string should be untouched, got %q", got)
	}
	got := TruncateMiddle("abcdefghijklmnop", 9)
	if utf8.RuneCountInString(got) != 9 {
		t.Errorf("TruncateMiddle width = %d, want 9 (%q)", utf8.RuneCountInString(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected an ellipsis in %q", got)
	}
	if !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "p") {
		t.Errorf("expected head and tail preserved, got %q", got)
	}
}

func TestDetailTableContainsPIDsAndCommands(t *testing.T) {
	rows := []DetailRow{
		{PID: 111, CPUText: "90%", CPULevel: High, MemText: "1.0G", MemLevel: Med, Command: "node vite"},
		{PID: 222, CPUText: "2%", CPULevel: Low, MemText: "50M", MemLevel: Low, Command: "node tsserver"},
	}
	out := DetailTable(rows)
	for _, want := range []string{"PID", "COMMAND", "111", "222", "node vite", "node tsserver"} {
		if !strings.Contains(out, want) {
			t.Errorf("DetailTable missing %q:\n%s", want, out)
		}
	}
}

func TestTableContainsAppNamesInOrder(t *testing.T) {
	rows := []Row{
		{App: "Google Chrome", CPUText: "120%", CPULevel: High, MemText: "4.2G", MemLevel: High, Count: 20},
		{App: "Slack", CPUText: "5%", CPULevel: Low, MemText: "800M", MemLevel: Med, Count: 4},
	}
	out := Table(rows)
	if !strings.Contains(out, "APP") {
		t.Errorf("table missing APP header:\n%s", out)
	}
	iChrome := strings.Index(out, "Google Chrome")
	iSlack := strings.Index(out, "Slack")
	if iChrome < 0 || iSlack < 0 {
		t.Fatalf("table missing app names (chrome=%d slack=%d):\n%s", iChrome, iSlack, out)
	}
	if iChrome > iSlack {
		t.Errorf("app order wrong: Chrome should appear before Slack:\n%s", out)
	}
}
