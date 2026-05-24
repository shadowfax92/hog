package group

import (
	"testing"

	"hog/internal/proc"
)

func TestAppKeyOutermostBundleWins(t *testing.T) {
	// A nested helper .app must resolve to the outermost owning app.
	comm := "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/1/Helpers/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)"
	if got := AppKey(comm); got != "Google Chrome" {
		t.Errorf("AppKey = %q, want %q", got, "Google Chrome")
	}
}

func TestAppKeyFallsBackToBasename(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Cellar/node/22.0/bin/node": "node",
		"/usr/sbin/mDNSResponder":                 "mDNSResponder",
		"/sbin/launchd":                           "launchd",
	}
	for in, want := range cases {
		if got := AppKey(in); got != want {
			t.Errorf("AppKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAggregateSumsAndCounts(t *testing.T) {
	procs := []proc.Proc{
		{PID: 10, CPUPct: 12, RSSKiB: 100, Comm: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		{PID: 11, CPUPct: 8, RSSKiB: 200, Comm: "/Applications/Google Chrome.app/Contents/Helpers/Google Chrome Helper"},
		{PID: 20, CPUPct: 5, RSSKiB: 50, Comm: "/sbin/launchd"},
	}
	byApp := map[string]Group{}
	for _, g := range Aggregate(procs) {
		byApp[g.App] = g
	}
	chrome, ok := byApp["Google Chrome"]
	if !ok {
		t.Fatal("no Google Chrome group produced")
	}
	if chrome.Count != 2 {
		t.Errorf("chrome Count = %d, want 2", chrome.Count)
	}
	if chrome.CPUPct != 20 {
		t.Errorf("chrome CPUPct = %v, want 20", chrome.CPUPct)
	}
	if chrome.RSSKiB != 300 {
		t.Errorf("chrome RSSKiB = %d, want 300", chrome.RSSKiB)
	}
	if len(chrome.PIDs) != 2 {
		t.Errorf("chrome PIDs = %v, want 2 entries", chrome.PIDs)
	}
}

func TestSortByCPUThenByMem(t *testing.T) {
	groups := []Group{
		{App: "a", CPUPct: 5, RSSKiB: 900},
		{App: "b", CPUPct: 30, RSSKiB: 100},
		{App: "c", CPUPct: 10, RSSKiB: 500},
	}
	Sort(groups, false)
	if groups[0].App != "b" || groups[1].App != "c" || groups[2].App != "a" {
		t.Errorf("CPU sort = %s,%s,%s want b,c,a", groups[0].App, groups[1].App, groups[2].App)
	}
	Sort(groups, true)
	if groups[0].App != "a" || groups[1].App != "c" || groups[2].App != "b" {
		t.Errorf("mem sort = %s,%s,%s want a,c,b", groups[0].App, groups[1].App, groups[2].App)
	}
}

func TestMatchCaseInsensitiveSubstring(t *testing.T) {
	groups := []Group{
		{App: "Google Chrome", PIDs: []int{1, 2}},
		{App: "Slack", PIDs: []int{3}},
	}
	got := Match(groups, "CHROME")
	if len(got) != 1 || got[0].App != "Google Chrome" {
		t.Fatalf("Match(CHROME) = %+v, want [Google Chrome]", got)
	}
}
