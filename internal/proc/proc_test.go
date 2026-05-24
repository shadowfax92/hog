package proc

import (
	"testing"
	"time"
)

func TestParsePSPreservesCommandWithSpaces(t *testing.T) {
	raw := "  419 99805  11248   2:21.11 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper\n" +
		"    1     0  20000 106:13.47 /sbin/launchd\n"
	got := parsePS(raw)
	if len(got) != 2 {
		t.Fatalf("parsePS returned %d procs, want 2", len(got))
	}
	if got[0].pid != 419 || got[0].ppid != 99805 || got[0].rssKiB != 11248 {
		t.Errorf("first proc fields wrong: %+v", got[0])
	}
	wantComm := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper"
	if got[0].comm != wantComm {
		t.Errorf("comm = %q, want %q", got[0].comm, wantComm)
	}
}

func TestParsePSSkipsBlankAndGarbage(t *testing.T) {
	raw := "\n   \ngarbage line here\n  2  1  100  0:01.00 /bin/foo\n"
	got := parsePS(raw)
	if len(got) != 1 {
		t.Fatalf("parsePS returned %d procs, want 1 (blank+garbage skipped)", len(got))
	}
	if got[0].pid != 2 {
		t.Errorf("pid = %d, want 2", got[0].pid)
	}
}

func TestParseCPUTime(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"12:34.56", 754.56},
		{"1:02:03", 3723},
		{"2-03:04:05", 183845},
		{"106:13.47", 6373.47}, // macOS ps minutes are unbounded, not rolled into hours
		{"0:00.03", 0.03},
	}
	for _, c := range cases {
		got := parseCPUTime(c.in)
		if d := got - c.want; d > 0.001 || d < -0.001 {
			t.Errorf("parseCPUTime(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSampleComputesWindowedCPU(t *testing.T) {
	first := []snapshotProc{
		{pid: 1, ppid: 0, rssKiB: 100, cpuSec: 10, comm: "/bin/a"},
		{pid: 2, ppid: 0, rssKiB: 200, cpuSec: 50, comm: "/bin/b"},
	}
	second := []snapshotProc{
		{pid: 1, ppid: 0, rssKiB: 150, cpuSec: 15, comm: "/bin/a"},  // +5s cpu over 5s window = 100%
		{pid: 2, ppid: 0, rssKiB: 250, cpuSec: 50, comm: "/bin/b"},  // +0 = 0%
		{pid: 3, ppid: 0, rssKiB: 300, cpuSec: 2.5, comm: "/bin/c"}, // new this window: 2.5/5 = 50%
	}
	got := sampleFrom(first, second, 5*time.Second)
	if len(got) != 3 {
		t.Fatalf("got %d procs, want 3 (one per second-snapshot entry)", len(got))
	}
	byPID := map[int]Proc{}
	for _, p := range got {
		byPID[p.PID] = p
	}
	if p := byPID[1]; p.CPUPct < 99.9 || p.CPUPct > 100.1 {
		t.Errorf("pid1 CPUPct = %v, want ~100", p.CPUPct)
	}
	if p := byPID[1]; p.RSSKiB != 150 {
		t.Errorf("pid1 RSSKiB = %d, want 150 (latest snapshot)", p.RSSKiB)
	}
	if p := byPID[2]; p.CPUPct != 0 {
		t.Errorf("pid2 CPUPct = %v, want 0", p.CPUPct)
	}
	if p := byPID[3]; p.CPUPct < 49.9 || p.CPUPct > 50.1 {
		t.Errorf("pid3 (new) CPUPct = %v, want ~50", p.CPUPct)
	}
}
