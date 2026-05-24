// Package proc samples the macOS process table and derives per-process CPU%
// over a sampling window by diffing cumulative CPU time across two snapshots.
package proc

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Proc is a sampled process with CPU% measured over the sampling window.
// Comm is the executable path and may contain spaces.
type Proc struct {
	PID    int
	PPID   int
	RSSKiB int64
	CPUPct float64
	Comm   string
}

// snapshotProc is one process at a single instant, before CPU% is derived.
// cpuSec is cumulative CPU time (seconds) as reported by ps.
type snapshotProc struct {
	pid    int
	ppid   int
	rssKiB int64
	cpuSec float64
	comm   string
}

// Sample reads the process table, waits d, reads it again, and returns each
// process with CPU% measured over the actual elapsed window.
func Sample(d time.Duration) ([]Proc, error) {
	first, err := snapshot()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	time.Sleep(d)
	second, err := snapshot()
	if err != nil {
		return nil, err
	}
	return sampleFrom(first, second, time.Since(start)), nil
}

// List takes a single snapshot of the process table without a sampling window.
// CPUPct is left 0 — use Sample when CPU% matters. Suited to the kill path,
// which only needs current PIDs and command paths.
func List() ([]Proc, error) {
	snaps, err := snapshot()
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(snaps))
	for _, p := range snaps {
		out = append(out, Proc{PID: p.pid, PPID: p.ppid, RSSKiB: p.rssKiB, Comm: p.comm})
	}
	return out, nil
}

// Terminate sends SIGTERM to each pid, waits a short grace period, then SIGKILL
// to any still alive. Per-PID errors (already exited, not permitted) are ignored
// so one stubborn process can't abort the rest.
func Terminate(pids []int) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(1500 * time.Millisecond)
	for _, pid := range pids {
		if syscall.Kill(pid, syscall.Signal(0)) == nil { // still alive
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// Commands returns the full command line (with arguments) for each pid, used to
// tell apart same-executable processes (e.g. which script each `node` runs).
// Dead pids are simply absent — ps may exit non-zero for them, which is fine.
func Commands(pids []int) map[int]string {
	if len(pids) == 0 {
		return map[int]string{}
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	out, _ := exec.Command("ps", "-ww", "-o", "pid=,command=", "-p", strings.Join(ids, ",")).Output()
	return parseCommands(string(out))
}

// parseCommands maps pid -> full command line from `ps -o pid=,command=` output.
// The command (everything after the pid) is kept verbatim, spaces and all.
func parseCommands(raw string) map[int]string {
	m := make(map[int]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:sp])
		if err != nil {
			continue
		}
		m[pid] = strings.TrimSpace(line[sp+1:])
	}
	return m
}

// sampleFrom is the pure CPU math: CPU% = (cpu2-cpu1)/elapsed*100 per PID.
// A PID only in second is treated as born mid-window (cpu1 = 0). RSS and Comm
// come from the latest (second) snapshot.
func sampleFrom(first, second []snapshotProc, elapsed time.Duration) []Proc {
	prev := make(map[int]float64, len(first))
	for _, p := range first {
		prev[p.pid] = p.cpuSec
	}
	secs := elapsed.Seconds()
	if secs <= 0 {
		secs = 1
	}
	out := make([]Proc, 0, len(second))
	for _, p := range second {
		delta := p.cpuSec - prev[p.pid]
		if delta < 0 {
			delta = 0
		}
		out = append(out, Proc{
			PID:    p.pid,
			PPID:   p.ppid,
			RSSKiB: p.rssKiB,
			CPUPct: delta / secs * 100,
			Comm:   p.comm,
		})
	}
	return out
}

// snapshot runs ps once. `-ww` prevents column truncation; trailing `=` on each
// -o field drops headers; comm is last so its embedded spaces don't break the
// leading numeric columns.
func snapshot() ([]snapshotProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,rss=,time=,comm=").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ps snapshot: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return parsePS(string(out)), nil
}

// parsePS turns ps output into snapshots. The first four whitespace-delimited
// fields are pid/ppid/rss/time; everything after is comm (path with spaces).
func parsePS(raw string) []snapshotProc {
	var procs []snapshotProc
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		rss, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		procs = append(procs, snapshotProc{
			pid:    pid,
			ppid:   ppid,
			rssKiB: rss,
			cpuSec: parseCPUTime(fields[3]),
			comm:   strings.Join(fields[4:], " "),
		})
	}
	return procs
}

// parseCPUTime converts a macOS ps TIME field to seconds. Handles "MM:SS.ss"
// (minutes unbounded — ps does not roll minutes into hours), "HH:MM:SS", and a
// leading "DD-" days prefix ("DD-HH:MM:SS").
func parseCPUTime(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var days float64
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		if d, err := strconv.ParseFloat(s[:dash], 64); err == nil {
			days = d
		}
		s = s[dash+1:]
	}
	var total float64
	for _, p := range strings.Split(s, ":") {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return days * 86400
		}
		total = total*60 + v
	}
	return days*86400 + total
}
