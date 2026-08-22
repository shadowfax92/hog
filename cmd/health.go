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

var flagHealthDuration int

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Weigh every subsystem and report whether the machine is in trouble",
	Long: "health samples the kernel's memory, swap, CPU and filesystem counters over a\n" +
		"window and scores each one, then combines them into a single verdict.\n\n" +
		"The window matters: swap and paging counters are cumulative since boot, so\n" +
		"only the change across an interval distinguishes a machine thrashing now\n" +
		"from one that thrashed days ago.",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runHealth,
}

func init() {
	healthCmd.Flags().IntVarP(&flagHealthDuration, "duration", "d", 15, "sampling window in seconds (min 1)")
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
		Pressure: health.ReadPressureLevel(),
		Load:     health.ReadLoadAvg(),
		NumCPU:   runtime.NumCPU(),
		TotalRAM: uint64(totalRAMBytes()),
		Procs:    len(procs),
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
	in.ReclaimBytes, in.ReclaimCount = reclaimable(procs)

	report := health.Evaluate(in)
	printHealth(out, report)
	return nil
}

// reclaimable measures memory held by dormant processes using the same
// selection `hog reap` acts on, so health's remedy and reap's behaviour cannot
// drift apart. Probes are deliberately not run here: health only reports a
// magnitude, and probing hundreds of processes would be a large side effect
// for a read-only command.
func reclaimable(procs []proc.Proc) (uint64, int) {
	cfg, _, err := reap.LoadConfig(reap.ConfigPath())
	if err != nil {
		cfg = reap.Config{}
	}
	crit, err := criteriaFrom(cfg)
	if err != nil {
		return 0, 0
	}
	res := reap.Select(procs, crit, currentPID(), cfg.Protect)
	return uint64(res.Freed()) * 1024, len(res.Candidates)
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

	// Lead with the failing subsystem: the composite score says how bad things
	// are, but not what to do about it.
	worst, ok := r.Worst()
	if !ok {
		fmt.Fprintln(w, render.Hint("\nNothing is under pressure."))
		return
	}
	fmt.Fprintf(w, "\n%s is the bottleneck: %s\n", worst.Name, worst.Detail)

	// Then every other actionable remedy, so a second problem is not hidden
	// behind the first.
	printed := 0
	for _, c := range append([]health.Check{worst}, r.Checks...) {
		if c.Remedy == "" || c.Level == health.Good || printed >= 3 {
			continue
		}
		if printed > 0 && c.Name == worst.Name {
			continue
		}
		fmt.Fprintln(w, render.Hint("→ "+c.Remedy))
		printed++
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
