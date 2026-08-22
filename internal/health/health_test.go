package health

import (
	"fmt"

	"hog/internal/proc"
	"testing"
	"time"
)

const pageSize = 16384

// healthy describes a machine with nothing wrong, so each test can break one
// dimension and confirm that dimension alone changes the outcome.
func healthy() Inputs {
	totalPages := uint64(8 * 1024 * 1024 * 1024 / pageSize * 16) // 128 GiB worth
	return Inputs{
		Window:   15 * time.Second,
		VM0:      VMStat{PageSize: pageSize},
		VM1:      VMStat{PageSize: pageSize, Free: totalPages / 2},
		CPU0:     CPUTicks{},
		CPU1:     CPUTicks{User: 500, System: 200, Idle: 9300},
		Swap:     SwapUsage{Total: 16 << 30, Used: 1 << 30, Avail: 15 << 30},
		Pressure: PressureNormal,
		Load:     [3]float64{2, 2, 2},
		NumCPU:   16,
		TotalRAM: 128 << 30,
		Disk:     DiskUsage{Free: 800 << 30, Total: 2000 << 30},
		Procs:    400,
	}
}

func checkNamed(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	panic("no check named " + name)
}

func TestHealthyMachineScoresWell(t *testing.T) {
	r := Evaluate(healthy())
	if r.Verdict != Healthy {
		t.Errorf("verdict = %v (score %d), want HEALTHY", r.Verdict, r.Score)
	}
	for _, c := range r.Checks {
		if c.Level != Good {
			t.Errorf("check %q = %v on a healthy machine (detail: %s)", c.Name, c.Level, c.Detail)
		}
	}
}

// Swap danger is absolute, not proportional: what matters is bytes remaining.
func TestSwapHeadroomIsCriticalNearExhaustion(t *testing.T) {
	in := healthy()
	in.Swap = SwapUsage{Total: 64 << 30, Used: 63<<30 + 200<<20, Avail: 824 << 20}
	c := checkNamed(Evaluate(in), "swap headroom")
	if c.Level != Critical {
		t.Errorf("level = %v with 824M of swap left, want Critical", c.Level)
	}
	if c.Score > 15 {
		t.Errorf("score = %d, want near zero", c.Score)
	}
}

func TestNoSwapFileIsHealthy(t *testing.T) {
	in := healthy()
	in.Swap = SwapUsage{}
	c := checkNamed(Evaluate(in), "swap headroom")
	if c.Level != Good || c.Score != 100 {
		t.Errorf("an unused swap file should score 100/Good, got %d/%v", c.Score, c.Level)
	}
}

// The rate, not the cumulative total, is what distinguishes thrashing now from
// thrashing days ago — this is why health samples over a window.
func TestSwapActivityUsesRateNotTotal(t *testing.T) {
	in := healthy()
	in.VM0 = VMStat{PageSize: pageSize, Swapins: 5_000_000, Swapouts: 5_000_000}
	in.VM1 = VMStat{PageSize: pageSize, Free: 1 << 20, Swapins: 5_000_000, Swapouts: 5_000_000}
	if c := checkNamed(Evaluate(in), "swap activity"); c.Level != Good {
		t.Errorf("huge lifetime totals with no movement in the window should be Good, got %v", c.Level)
	}

	in.VM1.Swapins = 5_000_000 + 9_000
	in.VM1.Swapouts = 5_000_000 + 9_000
	if c := checkNamed(Evaluate(in), "swap activity"); c.Level != Critical {
		t.Errorf("1200 pages/s should be Critical, got %v", c.Level)
	}
}

// Kernel counters are not sampled atomically and can appear to move backwards;
// an unsigned wrap would turn that into an astronomical rate.
func TestCountersGoingBackwardsDoNotWrap(t *testing.T) {
	in := healthy()
	in.VM0 = VMStat{PageSize: pageSize, Swapins: 100, Swapouts: 100}
	in.VM1 = VMStat{PageSize: pageSize, Free: 1 << 20, Swapins: 50, Swapouts: 50}
	if c := checkNamed(Evaluate(in), "swap activity"); c.Level != Good {
		t.Errorf("a backwards counter should read as no activity, got %v (%s)", c.Level, c.Detail)
	}
}

// The kernel's own pressure verdict outranks any threshold hog could invent.
func TestKernelPressureLevelCapsSeverity(t *testing.T) {
	in := healthy()
	in.Pressure = PressureCritical
	c := checkNamed(Evaluate(in), "memory pressure")
	if c.Level != Critical {
		t.Errorf("level = %v, want Critical when the kernel says so", c.Level)
	}
	if c.Score > 10 {
		t.Errorf("score = %d, want capped low under critical pressure", c.Score)
	}
}

func TestKernelTimeFlagsVMDistress(t *testing.T) {
	in := healthy()
	// Mostly idle overall, but half of the busy time is kernel: the signature
	// of a machine managing memory rather than doing work.
	in.CPU1 = CPUTicks{User: 2000, System: 4900, Idle: 3100}
	c := checkNamed(Evaluate(in), "kernel time")
	if c.Level != Critical {
		t.Errorf("level = %v at 49%% system time, want Critical", c.Level)
	}
}

func TestReclaimableCarriesItsOwnRemedy(t *testing.T) {
	in := healthy()
	in.ReclaimBytes = 60 << 30 // ~47% of 128 GiB
	in.ReclaimCount = 41
	c := checkNamed(Evaluate(in), "reclaimable")
	if c.Level != Critical {
		t.Errorf("level = %v, want Critical", c.Level)
	}
	if c.Remedy == "" {
		t.Error("reclaimable should carry an actionable remedy")
	}
}

// A failing subsystem must not be averaged away by healthy ones.
func TestCriticalCheckCapsVerdict(t *testing.T) {
	in := healthy()
	in.Swap = SwapUsage{Total: 64 << 30, Used: 64 << 30, Avail: 100 << 20}
	r := Evaluate(in)
	if r.Verdict == Healthy {
		t.Errorf("verdict = HEALTHY (score %d) despite a critical subsystem", r.Score)
	}
}

func TestTwoCriticalsAreUnhealthy(t *testing.T) {
	in := healthy()
	in.Swap = SwapUsage{Total: 64 << 30, Used: 64 << 30, Avail: 100 << 20}
	in.Pressure = PressureCritical
	if r := Evaluate(in); r.Verdict != Unhealthy {
		t.Errorf("verdict = %v with two critical subsystems, want UNHEALTHY", r.Verdict)
	}
}

func TestWorstPicksHighestSeverity(t *testing.T) {
	r := Report{Checks: []Check{
		{Name: "low score but fine", Score: 20, Level: Good},
		{Name: "the real problem", Score: 60, Level: Critical},
		{Name: "warned", Score: 30, Level: Warn},
	}}
	worst, ok := r.Worst()
	if !ok || worst.Name != "the real problem" {
		t.Errorf("Worst() = %q, want the critical check regardless of score", worst.Name)
	}
}

func TestWorstReportsNothingWhenAllGood(t *testing.T) {
	r := Report{Checks: []Check{{Name: "a", Score: 90, Level: Good}}}
	if _, ok := r.Worst(); ok {
		t.Error("Worst() should report no bottleneck when every check is Good")
	}
}

func TestScoreRange(t *testing.T) {
	// Smaller-is-better: best=0, worst=1000.
	cases := []struct {
		v, best, worst float64
		want           int
	}{
		{0, 0, 1000, 100},
		{1000, 0, 1000, 0},
		{500, 0, 1000, 50},
		{2000, 0, 1000, 0}, // clamped
		{-5, 0, 1000, 100}, // clamped
		{8, 8, 0, 100},     // larger-is-better
		{0, 8, 0, 0},
		{4, 8, 0, 50},
	}
	for _, c := range cases {
		if got := scoreRange(c.v, c.best, c.worst); got != c.want {
			t.Errorf("scoreRange(%v, %v, %v) = %d, want %d", c.v, c.best, c.worst, got, c.want)
		}
	}
}

// Attribution turns "swap is full" into "rust-analyzer is what is in it",
// which is the difference between a symptom and a diagnosis.
func TestSwapHeadroomNamesTheProcessesFillingIt(t *testing.T) {
	in := healthy()
	in.Swap = SwapUsage{Total: 64 << 30, Used: 63 << 30, Avail: 900 << 20}
	in.Processes = []proc.Proc{
		// Almost entirely evicted: 8 GiB footprint, 100 MiB resident.
		{PID: 1, Comm: "/bin/rust-analyzer", FootprintKiB: 8 << 20, ResidentKiB: 100 << 10},
		{PID: 2, Comm: "/bin/rust-analyzer", FootprintKiB: 8 << 20, ResidentKiB: 100 << 10},
		// Large but fully resident: not in swap, so not a cause.
		{PID: 3, Comm: "/bin/browser", FootprintKiB: 12 << 20, ResidentKiB: 12 << 20},
	}
	c := checkNamed(Evaluate(in), "swap headroom")
	if len(c.Causes) == 0 {
		t.Fatal("expected the check to name what is filling swap")
	}
	if c.Causes[0].Label != "rust-analyzer" {
		t.Errorf("top cause = %q, want rust-analyzer", c.Causes[0].Label)
	}
	for _, cause := range c.Causes {
		if cause.Label == "browser" {
			t.Error("a fully resident process is not in swap and must not be blamed for it")
		}
	}
	if c.Because == "" {
		t.Error("the check should explain the mechanism, not just the number")
	}
}

// Memory pressure ranks by total footprint, where swap ranks by evicted bytes:
// the same process table produces different leaders.
func TestMemoryPressureRanksByFootprintNotEviction(t *testing.T) {
	in := healthy()
	in.Processes = []proc.Proc{
		{PID: 1, Comm: "/bin/evicted", FootprintKiB: 4 << 20, ResidentKiB: 0},
		{PID: 2, Comm: "/bin/resident", FootprintKiB: 12 << 20, ResidentKiB: 12 << 20},
	}
	c := checkNamed(Evaluate(in), "memory pressure")
	if len(c.Causes) == 0 || c.Causes[0].Label != "resident" {
		t.Errorf("top cause = %+v, want the largest footprint", c.Causes)
	}
}

// Checks measuring a whole-machine property must not invent an attribution.
func TestUnattributableChecksCarryNoCauses(t *testing.T) {
	in := healthy()
	in.Processes = []proc.Proc{{PID: 1, Comm: "/bin/x", FootprintKiB: 1 << 20}}
	r := Evaluate(in)
	for _, name := range []string{"swap activity", "kernel time", "disk headroom"} {
		if c := checkNamed(r, name); len(c.Causes) != 0 {
			t.Errorf("%s should have no per-process causes, got %d", name, len(c.Causes))
		} else if c.Because == "" {
			t.Errorf("%s should still explain itself", name)
		}
	}
}

func TestEveryCheckExplainsItself(t *testing.T) {
	for _, c := range Evaluate(healthy()).Checks {
		if c.Because == "" {
			t.Errorf("check %q has no explanation", c.Name)
		}
	}
}

func TestCausesAreBounded(t *testing.T) {
	in := healthy()
	for i := range 40 {
		in.Processes = append(in.Processes, proc.Proc{
			PID: i + 1, Comm: fmt.Sprintf("/bin/app%02d", i), FootprintKiB: int64(i+1) << 18,
		})
	}
	if c := checkNamed(Evaluate(in), "memory pressure"); len(c.Causes) > maxCauses {
		t.Errorf("got %d causes, want at most %d", len(c.Causes), maxCauses)
	}
}
