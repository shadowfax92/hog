// Package proc samples the macOS process table. It derives per-process CPU%
// over a sampling window by diffing cumulative CPU time across two snapshots,
// and reads each process's true memory footprint from the kernel.
//
// Memory accounting note: this package deliberately does not rank by RSS.
// macOS compresses and swaps idle pages, so a dormant process holding
// gigabytes reports a near-zero RSS while still occupying the machine's memory
// ceiling. See proc.Stats for the accounting actually used.
package proc

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Proc is a sampled process. CPUPct is measured over the sampling window;
// every other field is an instantaneous read from the latest snapshot.
// Comm is the executable path and may contain spaces.
type Proc struct {
	PID    int
	PPID   int
	Comm   string
	CPUPct float64

	// FootprintKiB is the kernel's phys_footprint when Measured, and the ps
	// RSS otherwise. Both are KiB so render code has one unit to format.
	FootprintKiB int64
	PeakKiB      int64
	ResidentKiB  int64

	Age      time.Duration // wall-clock since process start
	CPUTotal time.Duration // lifetime user+system CPU
	Threads  int           // live threads

	// Measured reports whether kernel accounting was readable. The kernel
	// denies proc_pid_rusage for processes owned by other users, so Measured
	// is true exactly for the caller's own processes. Commands that kill
	// treat this as a safety boundary: an unmeasurable process is somebody
	// else's (root, _windowserver, …) and is never a reap candidate. This
	// makes the permission model the protection model, with no blocklist to
	// maintain.
	Measured bool
}

// Duty is the fraction of its lifetime a process has spent on-CPU. It
// separates dormant processes from working ones far more reliably than an
// instantaneous CPU reading, where everything looks idle inside a few seconds:
// a language server that indexed once and then slept for two days sits near
// 0.2%, while an interactive process stays above 1%.
func (p Proc) Duty() float64 {
	if p.Age <= 0 {
		return 0
	}
	return float64(p.CPUTotal) / float64(p.Age)
}

// ColdFrac estimates the share of a process's footprint the kernel has
// compressed or swapped out. It is diagnostic only, never a kill criterion:
// the estimate is exact for genuinely swapped processes but overstates
// coldness for GPU-backed apps, whose footprint includes charged pages that
// were never resident to begin with.
func (p Proc) ColdFrac() float64 {
	if p.FootprintKiB <= 0 {
		return 0
	}
	cold := p.FootprintKiB - p.ResidentKiB
	if cold < 0 {
		return 0
	}
	return float64(cold) / float64(p.FootprintKiB)
}

// snapshotProc is one process at a single instant, before CPU% is derived.
// cpuSec is cumulative CPU time (seconds) since exec.
type snapshotProc struct {
	pid    int
	ppid   int
	rssKiB int64
	cpuSec float64
	comm   string
	stats  Stats
	ok     bool // kernel accounting was readable
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

// List takes a single snapshot without a sampling window. CPUPct is left 0 —
// use Sample when CPU% matters. Suited to the kill path, which only needs
// current PIDs, footprints, and command paths.
func List() ([]Proc, error) {
	snaps, err := snapshot()
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(snaps))
	for _, p := range snaps {
		out = append(out, newProc(p, 0))
	}
	return out, nil
}

// newProc projects a snapshot into a Proc, preferring kernel accounting and
// falling back to ps RSS for processes the kernel won't report on.
func newProc(p snapshotProc, cpuPct float64) Proc {
	out := Proc{
		PID:          p.pid,
		PPID:         p.ppid,
		Comm:         p.comm,
		CPUPct:       cpuPct,
		FootprintKiB: p.rssKiB,
		Measured:     p.ok,
	}
	if p.ok {
		out.FootprintKiB = p.stats.Footprint / 1024
		out.PeakKiB = p.stats.PeakFootprint / 1024
		out.ResidentKiB = p.stats.Resident / 1024
		out.Age = p.stats.Age
		out.CPUTotal = p.stats.CPU
		out.Threads = p.stats.Threads
	}
	return out
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
// A PID only in second is treated as born mid-window (cpu1 = 0). All other
// fields come from the latest (second) snapshot.
func sampleFrom(first, second []snapshotProc, elapsed time.Duration) []Proc {
	prev := make(map[int]float64, len(first))
	for _, p := range first {
		prev[p.pid] = p.cpuSeconds()
	}
	secs := elapsed.Seconds()
	if secs <= 0 {
		secs = 1
	}
	out := make([]Proc, 0, len(second))
	for _, p := range second {
		delta := p.cpuSeconds() - prev[p.pid]
		if delta < 0 {
			delta = 0
		}
		out = append(out, newProc(p, delta/secs*100))
	}
	return out
}

// cpuSeconds prefers the kernel's nanosecond CPU total over the ps TIME field,
// whose one-hundredth-of-a-second resolution is too coarse to distinguish a
// dormant process from an idle one over a short window.
func (p snapshotProc) cpuSeconds() float64 {
	if p.ok {
		return p.stats.CPU.Seconds()
	}
	return p.cpuSec
}

// snapshot runs ps once for enumeration, then reads kernel accounting per pid.
// ps supplies pid/ppid/comm (and an RSS fallback for other users' processes);
// everything that drives ranking and reaping comes from the kernel.
func snapshot() ([]snapshotProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,rss=,time=,comm=").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ps snapshot: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	procs := parsePS(string(out))
	for i := range procs {
		procs[i].stats, procs[i].ok = ReadStats(procs[i].pid)
	}
	return procs, nil
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
