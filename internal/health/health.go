// Package health turns raw kernel counters into a weighted verdict on whether
// the machine is in trouble, and if so which subsystem is responsible.
//
// The design principle is that a single number must never hide a failing
// subsystem. A machine one gigabyte from swap exhaustion is in serious trouble
// no matter how healthy its CPU and disk look, so the composite score is
// reported alongside per-check levels, and any critical check caps the verdict
// regardless of what the average says.
package health

import (
	"fmt"
	"time"

	"hog/internal/proc"
)

// Level is one check's severity.
type Level int

const (
	Good Level = iota
	Warn
	Critical
)

// Verdict is the machine's overall state.
type Verdict int

const (
	Healthy Verdict = iota
	Degraded
	Unhealthy
)

func (v Verdict) String() string {
	switch v {
	case Healthy:
		return "HEALTHY"
	case Degraded:
		return "DEGRADED"
	default:
		return "UNHEALTHY"
	}
}

// Check is one dimension of machine health. Weight expresses how much the
// dimension matters to whether the machine actually feels slow: swap and
// memory dominate, process count is a weak hint.
type Check struct {
	Name   string
	Score  int // 0 (worst) to 100 (best)
	Level  Level
	Detail string
	Remedy string
	Weight int

	// Because states the mechanism: what this metric actually measures and why
	// it makes a machine feel slow. A score alone tells you something is wrong
	// without telling you what kind of wrong.
	Because string

	// Causes names the processes responsible, largest first, for the checks
	// where the pressure can be attributed. Checks measuring a whole-machine
	// property the kernel does not break down per process (paging rate, time
	// spent in the kernel, disk space) carry no causes, and say so rather than
	// inventing an attribution.
	Causes []Cause
}

// Cause is one contributor to a check, already formatted for display.
type Cause struct {
	Label string
	Value string
	Note  string
}

// Inputs are the measurements Evaluate scores. Counter pairs (VM0/VM1,
// CPU0/CPU1) bracket the sampling window so rates can be derived; a single
// reading of a cumulative counter says only what has happened since boot,
// which cannot distinguish a machine thrashing now from one that thrashed
// last Tuesday.
type Inputs struct {
	Window     time.Duration
	VM0, VM1   VMStat
	CPU0, CPU1 CPUTicks
	Swap       SwapUsage
	Pressure   int
	Load       [3]float64
	NumCPU     int
	TotalRAM   uint64
	Disk       DiskUsage
	Procs      int
	Threads    int

	// ReclaimBytes is memory held by processes that are old, dormant and
	// idle — waste rather than working set. It comes from the same selection
	// `hog reap` uses, so the remedy is exact rather than a guess.
	ReclaimBytes uint64
	ReclaimCount int

	// Processes is the sampled process table, used to attribute pressure to
	// the processes causing it. Reclaim is the subset `hog reap` would take.
	Processes []proc.Proc
	Reclaim   []proc.Proc
}

// Report is the outcome of one evaluation.
type Report struct {
	Checks  []Check
	Score   int
	Verdict Verdict
	Window  time.Duration
}

// Worst returns the check most responsible for the verdict: the highest
// severity, and within that the lowest score. It exists so output can lead
// with the actual problem instead of making the reader scan a table.
func (r Report) Worst() (Check, bool) {
	if len(r.Checks) == 0 {
		return Check{}, false
	}
	worst := r.Checks[0]
	for _, c := range r.Checks[1:] {
		if c.Level > worst.Level || (c.Level == worst.Level && c.Score < worst.Score) {
			worst = c
		}
	}
	return worst, worst.Level != Good
}

// Evaluate scores every dimension and combines them. It is pure: all I/O
// happens in the caller, so the scoring is testable against fabricated
// machines.
func Evaluate(in Inputs) Report {
	checks := []Check{
		swapHeadroom(in),
		swapActivity(in),
		memoryPressure(in),
		reclaimable(in),
		kernelTime(in),
		cpuLoad(in),
		processCount(in),
		diskHeadroom(in),
	}

	var weighted, weights int
	criticals := 0
	for _, c := range checks {
		weighted += c.Score * c.Weight
		weights += c.Weight
		if c.Level == Critical {
			criticals++
		}
	}
	score := 100
	if weights > 0 {
		score = weighted / weights
	}

	// A critical subsystem cannot be averaged away: two of them, or a badly
	// low composite, means the machine is in trouble whatever else is fine.
	verdict := Healthy
	switch {
	case criticals >= 2 || score < 40:
		verdict = Unhealthy
	case criticals >= 1 || score < 75:
		verdict = Degraded
	}
	return Report{Checks: checks, Score: score, Verdict: verdict, Window: in.Window}
}

// swapHeadroom scores how close the swap file is to exhaustion. The danger is
// absolute rather than proportional: when swap fills, macOS stops being
// graceful and begins killing processes, and that happens at zero bytes free
// whether the file is 4 GB or 64 GB.
func swapHeadroom(in Inputs) Check {
	c := Check{Name: "swap headroom", Weight: 3, Score: 100, Level: Good}
	c.Because = "Swap is the disk file holding pages evicted from RAM. It fills when " +
		"processes ask for more memory than the machine has, and when it runs out " +
		"macOS stops being graceful and starts killing things. The apps below hold " +
		"the most memory that is no longer resident, so they are what is in there."
	c.Causes = evictionCauses(in.Processes)
	if in.Swap.Total == 0 {
		c.Detail = "no swap file in use"
		c.Because = "macOS has not needed a swap file, which means RAM has been sufficient."
		c.Causes = nil
		return c
	}
	freeGB := float64(in.Swap.Avail) / gib
	c.Score = scoreRange(freeGB, 8, 0) // 8 GiB free is fine, 0 is fatal
	c.Detail = fmt.Sprintf("%s of %s used — %s free",
		humanBytes(in.Swap.Used), humanBytes(in.Swap.Total), humanBytes(in.Swap.Avail))
	switch {
	case freeGB < 1:
		c.Level, c.Remedy = Critical, "free memory now — at zero, macOS starts force-killing applications"
	case freeGB < 4:
		c.Level, c.Remedy = Warn, "reclaim memory so the kernel can shrink swap"
	}
	return c
}

// swapActivity measures paging to and from disk during the window. This is the
// difference between a machine that once swapped and one thrashing right now,
// and it is the reason health samples over an interval instead of snapshotting.
func swapActivity(in Inputs) Check {
	c := Check{Name: "swap activity", Weight: 3, Score: 100, Level: Good}
	c.Because = "Pages moving between RAM and disk during the window. A full swap file " +
		"is survivable if nothing is reading from it; sustained traffic is not, because " +
		"every page fault stalls a process on disk. The kernel does not attribute this " +
		"traffic per process, so there is no breakdown — read it alongside swap headroom."
	secs := in.Window.Seconds()
	if secs <= 0 {
		secs = 1
	}
	rate := float64(satSub(in.VM1.Swapins, in.VM0.Swapins)+satSub(in.VM1.Swapouts, in.VM0.Swapouts)) / secs
	c.Score = scoreRange(rate, 0, 1000)
	c.Detail = fmt.Sprintf("%.0f pages/s to and from swap", rate)
	switch {
	case rate > 500:
		c.Level, c.Remedy = Critical, "the machine is thrashing — processes are waiting on disk, not working"
	case rate > 100:
		c.Level, c.Remedy = Warn, "sustained paging; something is larger than RAM allows"
	}
	return c
}

// memoryPressure combines free memory with the kernel's own pressure verdict.
// The kernel's level is authoritative — it is the same signal macOS acts on
// when it decides to terminate applications — so it sets the severity while
// the free fraction sets the score.
func memoryPressure(in Inputs) Check {
	c := Check{Name: "memory pressure", Weight: 3, Score: 100, Level: Good}
	c.Because = "How much memory can still be handed out, with the severity set by the " +
		"kernel's own pressure level — the same signal macOS acts on when it decides to " +
		"terminate applications. These apps hold the largest footprints."
	c.Causes = footprintCauses(in.Processes)
	if in.TotalRAM == 0 {
		c.Detail = "unknown"
		return c
	}
	ps := in.VM1.PageSize
	// Inactive and purgeable pages are reclaimable on demand, so they count
	// as available rather than used.
	available := (in.VM1.Free + in.VM1.Inactive + in.VM1.Purgeable + in.VM1.Speculative) * ps
	frac := float64(available) / float64(in.TotalRAM)
	c.Score = scoreRange(frac*100, 40, 5)
	c.Detail = fmt.Sprintf("%s available of %s · %s compressed",
		humanBytes(available), humanBytes(in.TotalRAM), humanBytes(in.VM1.Compressor*ps))
	switch in.Pressure {
	case PressureCritical:
		c.Level, c.Remedy = Critical, "the kernel reports critical memory pressure"
		c.Score = min(c.Score, 10)
	case PressureWarning:
		c.Level, c.Remedy = Warn, "the kernel reports elevated memory pressure"
		c.Score = min(c.Score, 50)
	default:
		if frac < 0.10 {
			c.Level = Warn
		}
	}
	return c
}

// reclaimable reports how much memory is held by processes that are old,
// dormant and idle. Unlike the other checks this one has an exact remedy,
// because it is measured with the same selection `hog reap` acts on.
func reclaimable(in Inputs) Check {
	c := Check{Name: "reclaimable", Weight: 2, Score: 100, Level: Good}
	c.Because = "Memory held by processes that are old, have spent almost none of their " +
		"life on CPU, and are idle now — waste rather than working set. This is exactly " +
		"what `hog reap` would take."
	c.Causes = footprintCauses(in.Reclaim)
	if in.TotalRAM == 0 || in.ReclaimCount == 0 {
		c.Detail = "nothing dormant worth reclaiming"
		return c
	}
	frac := float64(in.ReclaimBytes) / float64(in.TotalRAM)
	c.Score = scoreRange(frac*100, 5, 60)
	c.Detail = fmt.Sprintf("%s held by %d dormant process(es)", humanBytes(in.ReclaimBytes), in.ReclaimCount)
	if frac > 0.15 {
		c.Remedy = fmt.Sprintf("hog reap would free %s across %d process(es)",
			humanBytes(in.ReclaimBytes), in.ReclaimCount)
		c.Level = Warn
		if frac > 0.40 {
			c.Level = Critical
		}
	}
	return c
}

// kernelTime scores the share of CPU spent in the kernel. A high system-time
// fraction on an otherwise idle machine is the signature of a VM subsystem in
// distress: the cores are busy compressing and faulting pages rather than
// running anyone's code.
func kernelTime(in Inputs) Check {
	c := Check{Name: "kernel time", Weight: 2, Score: 100, Level: Good}
	c.Because = "The share of CPU spent inside the kernel rather than in your programs. " +
		"A high figure on an otherwise idle machine means the cores are busy compressing " +
		"and faulting pages instead of running anyone's code. Kernel time belongs to no " +
		"single process, so it has no breakdown; it corroborates the swap checks."
	d := in.CPU1.Sub(in.CPU0)
	total := d.Total()
	if total == 0 {
		c.Detail = "no samples"
		return c
	}
	sys := float64(d.System) / float64(total) * 100
	busy := float64(d.User+d.System+d.Nice) / float64(total) * 100
	c.Score = scoreRange(sys, 10, 50)
	c.Detail = fmt.Sprintf("%.0f%% system, %.0f%% busy overall", sys, busy)
	switch {
	case sys > 40:
		c.Level, c.Remedy = Critical, "the kernel is spending its time on memory management, not your work"
	case sys > 20:
		c.Level = Warn
	}
	return c
}

// cpuLoad scores the run queue relative to core count. Load is reported
// alongside idle time because the two disagreeing — a high load on an idle
// CPU — means processes are blocked on I/O rather than computing.
func cpuLoad(in Inputs) Check {
	c := Check{Name: "cpu load", Weight: 2, Score: 100, Level: Good}
	c.Because = "How many processes want to run at once, against the number of cores. " +
		"Load counts processes blocked on disk as well as computing, so a high load with " +
		"an idle CPU means they are waiting on paging, not working."
	c.Causes = cpuCauses(in.Processes)
	if in.NumCPU == 0 {
		c.Detail = "unknown"
		return c
	}
	per := in.Load[0] / float64(in.NumCPU)
	c.Score = scoreRange(per, 0.7, 2.0)
	c.Detail = fmt.Sprintf("%.1f on %d cores (%.2f per core)", in.Load[0], in.NumCPU, per)
	switch {
	case per > 2.0:
		c.Level, c.Remedy = Critical, "far more runnable work than cores"
	case per > 1.0:
		c.Level = Warn
	}
	return c
}

// processCount is a weak signal and weighted accordingly: a large count is
// normal on a developer machine and only hints at accumulation.
func processCount(in Inputs) Check {
	c := Check{Name: "process count", Weight: 1, Score: 100, Level: Good}
	c.Because = "A large count is normal on a developer machine, so this is a weak signal " +
		"and weighted accordingly. It matters when one program dominates the list: that is " +
		"something spawning processes faster than it reaps them."
	c.Causes = countCauses(in.Processes)
	c.Score = scoreRange(float64(in.Procs), 600, 2500)
	c.Detail = fmt.Sprintf("%d processes", in.Procs)
	if in.Threads > 0 {
		c.Detail += fmt.Sprintf(", %d threads", in.Threads)
	}
	switch {
	case in.Procs > 2000:
		c.Level, c.Remedy = Warn, "processes are accumulating faster than they exit"
	case in.Procs > 1200:
		c.Level = Warn
	}
	return c
}

// diskHeadroom scores free space on the data volume. It matters beyond storage
// because swap files live there: a full disk means swap cannot grow.
func diskHeadroom(in Inputs) Check {
	c := Check{Name: "disk headroom", Weight: 1, Score: 100, Level: Good}
	c.Because = "Free space on the data volume. It matters beyond storage because swap " +
		"files live there: on a full disk swap cannot grow, so memory pressure turns into " +
		"process termination sooner. Disk usage is not attributable to running processes."
	if in.Disk.Total == 0 {
		c.Detail = "unknown"
		return c
	}
	frac := float64(in.Disk.Free) / float64(in.Disk.Total)
	c.Score = scoreRange(frac*100, 20, 2)
	c.Detail = fmt.Sprintf("%s free of %s (%.0f%% used)",
		humanBytes(in.Disk.Free), humanBytes(in.Disk.Total), (1-frac)*100)
	switch {
	case frac < 0.03:
		c.Level, c.Remedy = Critical, "swap cannot grow on a full disk"
	case frac < 0.10:
		c.Level = Warn
	}
	return c
}

// scoreRange maps a measurement onto 0..100, where best scores 100 and worst
// scores 0, interpolating linearly between. best may be greater than worst,
// which expresses a metric where smaller is better.
func scoreRange(v, best, worst float64) int {
	if best == worst {
		return 100
	}
	frac := (v - worst) / (best - worst)
	switch {
	case frac >= 1:
		return 100
	case frac <= 0:
		return 0
	default:
		return int(frac * 100)
	}
}

const (
	kib = 1024.0
	mib = kib * 1024
	gib = mib * 1024
)

// humanBytes formats a byte count compactly.
func humanBytes(b uint64) string {
	switch v := float64(b); {
	case v >= gib:
		return fmt.Sprintf("%.1fG", v/gib)
	case v >= mib:
		return fmt.Sprintf("%.0fM", v/mib)
	case v >= kib:
		return fmt.Sprintf("%.0fK", v/kib)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
