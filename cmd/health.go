package cmd

import (
	"fmt"
	"io"
	"runtime"
	"time"

	"hog/internal/health"
	"hog/internal/proc"
	"hog/internal/reap"
	"hog/internal/render"

	"github.com/spf13/cobra"
)

var (
	flagHealthDuration int
	flagHealthExplain  bool
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Weigh every subsystem and report whether the machine is in trouble",
	Long: "health samples the kernel's memory, swap, CPU and filesystem counters over a\n" +
		"window and scores each one, then combines them into a single verdict.\n\n" +
		"The window matters: swap and paging counters are cumulative since boot, so\n" +
		"only the change across an interval distinguishes a machine thrashing now\n" +
		"from one that thrashed days ago.\n\n" +
		"Every check explains what it measures and, where the kernel makes it\n" +
		"attributable, which apps are responsible. Use -e to explain all of them.",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runHealth,
}

func init() {
	healthCmd.Flags().IntVarP(&flagHealthDuration, "duration", "d", 15, "sampling window in seconds (min 1)")
	healthCmd.Flags().BoolVarP(&flagHealthExplain, "explain", "e", false, "explain every check, not just the bottleneck")
	rootCmd.AddCommand(healthCmd)
}

// runHealth brackets the sampling window with kernel counter reads, scores the
// result, and prints the verdict with the responsible subsystem first.
func runHealth(cmd *cobra.Command, _ []string) error {
	dur := flagHealthDuration
	if dur < 1 {
		dur = 1
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sampling for %ds…\n", dur)

	vm0, err := health.ReadVM()
	if err != nil {
		return err
	}
	cpu0, err := health.ReadCPU()
	if err != nil {
		return err
	}

	// proc.Sample supplies the process-table half of the window and blocks for
	// its duration, so it doubles as the interval timer for the counter pair.
	start := time.Now()
	procs, err := proc.Sample(time.Duration(dur) * time.Second)
	if err != nil {
		return err
	}
	window := time.Since(start)

	vm1, err := health.ReadVM()
	if err != nil {
		return err
	}
	cpu1, err := health.ReadCPU()
	if err != nil {
		return err
	}

	in := health.Inputs{
		Window: window,
		VM0:    vm0, VM1: vm1,
		CPU0: cpu0, CPU1: cpu1,
		Pressure:  health.ReadPressureLevel(),
		Load:      health.ReadLoadAvg(),
		NumCPU:    runtime.NumCPU(),
		TotalRAM:  uint64(totalRAMBytes()),
		Procs:     len(procs),
		Processes: procs,
	}
	if sw, err := health.ReadSwap(); err == nil {
		in.Swap = sw
	}
	if d, err := health.ReadDisk("/System/Volumes/Data"); err == nil {
		in.Disk = d
	}
	for _, p := range procs {
		in.Threads += p.Threads
	}
	in.Reclaim = reclaimable(procs)
	in.ReclaimCount = len(in.Reclaim)
	for _, p := range in.Reclaim {
		in.ReclaimBytes += uint64(p.FootprintKiB) * 1024
	}

	report := health.Evaluate(in)
	printHealth(out, report)
	return nil
}

// reclaimable measures memory held by dormant processes using the same
// selection `hog reap` acts on, so health's remedy and reap's behaviour cannot
// drift apart. Probes are deliberately not run here: health only reports a
// magnitude, and probing hundreds of processes would be a large side effect
// for a read-only command.
func reclaimable(procs []proc.Proc) []proc.Proc {
	cfg, _, err := reap.LoadConfig(reap.ConfigPath())
	if err != nil {
		cfg = reap.Config{}
	}
	crit, err := criteriaFrom(cfg)
	if err != nil {
		return nil
	}
	res := reap.Select(procs, crit, currentPID(), cfg.Protect)
	out := make([]proc.Proc, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, c.Proc)
	}
	return out
}

func printHealth(w io.Writer, r health.Report) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, render.Verdict(r.Verdict.String(), r.Score, verdictLevel(r.Verdict)))
	fmt.Fprintln(w)

	rows := make([]render.HealthRow, 0, len(r.Checks))
	for _, c := range r.Checks {
		rows = append(rows, render.HealthRow{
			Name:   c.Name,
			Score:  c.Score,
			Level:  checkLevel(c.Level),
			Detail: c.Detail,
		})
	}
	fmt.Fprintln(w, render.HealthTable(rows))

	// -e explains every dimension; by default only the one that is actually
	// hurting, so a healthy machine stays a glance rather than an essay.
	if flagHealthExplain {
		for _, c := range r.Checks {
			explainCheck(w, c)
		}
		return
	}

	worst, ok := r.Worst()
	if !ok {
		fmt.Fprintln(w, render.Hint("\nNothing is under pressure."))
		fmt.Fprintln(w, render.Hint("tip: `hog health -e` explains what each check measures"))
		return
	}
	fmt.Fprintf(w, "\n%s is the bottleneck: %s\n", worst.Name, worst.Detail)
	explainBody(w, worst)

	// Then every other actionable remedy, so a second problem is not hidden
	// behind the first.
	printed := 0
	for _, c := range r.Checks {
		if c.Remedy == "" || c.Level == health.Good || c.Name == worst.Name || printed >= 2 {
			continue
		}
		fmt.Fprintln(w, render.Hint("→ "+c.Remedy))
		printed++
	}
	fmt.Fprintln(w, render.Hint("tip: `hog health -e` explains every check"))
}

// explainCheck prints one check's heading followed by its explanation.
func explainCheck(w io.Writer, c health.Check) {
	fmt.Fprintf(w, "\n%s — %s\n", render.CheckHeading(c.Name, checkLevel(c.Level)), c.Detail)
	explainBody(w, c)
}

// explainBody prints the mechanism, the attributable causes, and the remedy.
func explainBody(w io.Writer, c health.Check) {
	if c.Because != "" {
		fmt.Fprintln(w, render.Wrap(c.Because, 78, "  "))
	}
	for _, cause := range c.Causes {
		fmt.Fprintln(w, render.CauseLine(cause.Value, cause.Label, cause.Note))
	}
	if c.Remedy != "" {
		fmt.Fprintln(w, render.Hint("  → "+c.Remedy))
	}
}

// verdictLevel and checkLevel map health severities onto render's color levels.
func verdictLevel(v health.Verdict) render.Level {
	switch v {
	case health.Healthy:
		return render.Low
	case health.Degraded:
		return render.Med
	default:
		return render.High
	}
}

func checkLevel(l health.Level) render.Level {
	switch l {
	case health.Good:
		return render.Low
	case health.Warn:
		return render.Med
	default:
		return render.High
	}
}
