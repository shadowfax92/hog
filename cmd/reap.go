package cmd

import (
	"fmt"
	"io"
	"os"

	"strings"
	"time"

	"hog/internal/proc"
	"hog/internal/reap"
	"hog/internal/render"

	"github.com/spf13/cobra"
)

var (
	flagReapDuration int
	flagReapOlder    string
	flagReapDuty     string
	flagReapMinMem   string
	flagReapMaxCPU   float64
	flagReapExecute  bool
	flagReapPick     bool
	flagReapTree     bool
	flagReapAll      bool
)

var reapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Find and kill processes that are old, dormant, and expensive",
	Long: "reap finds processes that have been alive a long time, have spent almost\n" +
		"none of that time on-CPU, and are still holding significant memory — the\n" +
		"dormant language servers and helpers that accumulate over days of uptime and\n" +
		"end up filling swap.\n\n" +
		"It is agnostic about what a process is: selection rests on measured\n" +
		"properties, plus probes (configured, not hardcoded) that ask a process\n" +
		"directly whether it is safe to kill. reap is a dry run unless given -x.",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runReap,
}

func init() {
	reapCmd.Flags().IntVarP(&flagReapDuration, "duration", "d", 3, "sampling window in seconds (min 1)")
	reapCmd.Flags().StringVar(&flagReapOlder, "older", "", "minimum process age (e.g. 30m, 12h, 2d)")
	reapCmd.Flags().StringVar(&flagReapDuty, "duty", "", "max percent of lifetime spent on-CPU (e.g. 1.0)")
	reapCmd.Flags().StringVar(&flagReapMinMem, "min-mem", "", "minimum footprint to be worth reaping (e.g. 200M)")
	reapCmd.Flags().Float64Var(&flagReapMaxCPU, "max-cpu", 5, "max CPU% during the sampling window")
	reapCmd.Flags().BoolVarP(&flagReapExecute, "execute", "x", false, "actually terminate the candidates")
	reapCmd.Flags().BoolVarP(&flagReapPick, "pick", "i", false, "choose candidates in an fzf multi-select, then kill")
	reapCmd.Flags().BoolVar(&flagReapTree, "tree", false, "also take descendants of qualifying processes")
	reapCmd.Flags().BoolVar(&flagReapAll, "all", false, "list every candidate (default: top 25)")
	rootCmd.AddCommand(reapCmd)
}

const reapDisplayLimit = 25

// runReap samples the process table, applies the reap predicates and probes,
// and either reports what it would kill (the default) or kills it.
func runReap(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	cfg, created, err := reap.LoadConfig(reap.ConfigPath())
	if err != nil {
		return fmt.Errorf("reap config: %w", err)
	}
	if created {
		fmt.Fprintf(out, "Wrote default config to %s\n", reap.ConfigPath())
	}
	crit, err := criteriaFrom(cfg)
	if err != nil {
		return err
	}

	dur := flagReapDuration
	if dur < 1 {
		dur = 1
	}
	fmt.Fprintf(out, "Sampling for %ds…\n", dur)
	procs, err := proc.Sample(time.Duration(dur) * time.Second)
	if err != nil {
		return err
	}

	res := reap.Select(procs, crit, os.Getpid(), cfg.Protect)
	reap.ApplyProbes(&res, cfg.Probes, reap.DefaultProbeTimeout)

	printReapSummary(out, res, crit)
	if len(res.Candidates) == 0 {
		return nil
	}

	cmds := proc.Commands(reap.PIDs(res.Candidates))
	printReapTable(out, res, cmds)
	printProtected(out, res)

	switch {
	case flagReapPick:
		return reapViaPicker(out, res, cmds)
	case flagReapExecute:
		proc.Terminate(reap.PIDs(res.Candidates))
		fmt.Fprintf(out, "\nReaped %d process(es), releasing %s.\n",
			len(res.Candidates), render.HumanBytes(res.Freed()))
		return nil
	default:
		fmt.Fprintln(out)
		fmt.Fprintln(out, render.Hint("dry run — nothing killed.  -x to execute,  -i to pick interactively"))
		return nil
	}
}

// criteriaFrom layers explicit flags over the config file's defaults, which in
// turn back-stop to values that are conservative on a healthy machine.
func criteriaFrom(cfg reap.Config) (reap.Criteria, error) {
	older := firstNonEmpty(flagReapOlder, cfg.Defaults.Older, "12h")
	duty := firstNonEmpty(flagReapDuty, cfg.Defaults.Duty, "1.0")
	minMem := firstNonEmpty(flagReapMinMem, cfg.Defaults.MinMem, "200M")

	age, err := reap.ParseDuration(older)
	if err != nil {
		return reap.Criteria{}, err
	}
	mem, err := reap.ParseSize(minMem)
	if err != nil {
		return reap.Criteria{}, err
	}
	var dutyPct float64
	if _, err := fmt.Sscanf(strings.TrimSpace(duty), "%g", &dutyPct); err != nil {
		return reap.Criteria{}, fmt.Errorf("invalid duty %q (a percent, e.g. 1.0)", duty)
	}

	return reap.Criteria{
		MinAge:       age,
		MaxDuty:      dutyPct / 100,
		MaxCPU:       flagReapMaxCPU,
		MinFootprint: mem,
		Tree:         flagReapTree,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func printReapSummary(w io.Writer, res reap.Result, crit reap.Criteria) {
	fmt.Fprintf(w, "%d processes scanned, %d yours\n", res.Scanned, res.Yours)
	fmt.Fprintln(w, render.Hint(fmt.Sprintf(
		"criteria: older than %s · duty below %.2f%% · idle now · at least %s",
		reap.FormatDuration(crit.MinAge), crit.MaxDuty*100, render.HumanBytes(crit.MinFootprint))))

	if len(res.Candidates) == 0 {
		fmt.Fprintln(w, "\nNothing to reap.")
		return
	}
	freed := res.Freed()
	fmt.Fprintf(w, "\nreapable: %d processes · %s\n", len(res.Candidates), render.HumanBytes(freed))
	if total := totalRAMBytes(); total > 0 {
		ram := render.HumanBytes(total / 1024)
		// Footprint can exceed physical RAM: the excess is precisely what the
		// kernel has compressed and pushed to swap, so say that rather than
		// printing a bare percentage over 100 that reads like a bug.
		if ratio := float64(freed) * 1024 / float64(total); ratio > 1 {
			fmt.Fprintln(w, render.Hint(fmt.Sprintf(
				"%.1f× this machine's %s of RAM — the excess is what is sitting in swap", ratio, ram)))
		} else {
			fmt.Fprintln(w, render.Hint(fmt.Sprintf("%.0f%% of this machine's %s of RAM", ratio*100, ram)))
		}
	}
}

func printReapTable(w io.Writer, res reap.Result, cmds map[int]string) {
	shown := res.Candidates
	if !flagReapAll && len(shown) > reapDisplayLimit {
		shown = shown[:reapDisplayLimit]
	}

	rows := make([]render.ReapRow, 0, len(shown))
	for _, c := range shown {
		cmdline := cmds[c.PID]
		if cmdline == "" {
			cmdline = c.Comm
		}
		rows = append(rows, render.ReapRow{
			PID:      c.PID,
			MemText:  render.HumanBytes(c.FootprintKiB),
			MemLevel: render.LevelOfSize(c.FootprintKiB),
			AgeText:  reap.FormatDuration(c.Age),
			DutyText: fmt.Sprintf("%.2f%%", c.Duty()*100),
			ColdText: fmt.Sprintf("%.0f%%", c.ColdFrac()*100),
			Command:  render.ShortCommand(cmdline, 64),
		})
	}
	fmt.Fprintln(w, render.ReapTable(rows))
	if len(shown) < len(res.Candidates) {
		fmt.Fprintln(w, render.Hint(fmt.Sprintf("… and %d more (--all to list every one; all %d are included in -x/-i)",
			len(res.Candidates)-len(shown), len(res.Candidates))))
	}
}

// printProtected always reports what was spared, so protection is visible
// rather than a silent omission the user has to infer from a smaller total.
func printProtected(w io.Writer, res reap.Result) {
	if len(res.Protected) == 0 {
		return
	}
	byReason := map[string]int{}
	var order []string
	var held int64
	for _, p := range res.Protected {
		if byReason[p.Why] == 0 {
			order = append(order, p.Why)
		}
		byReason[p.Why]++
		held += p.FootprintKiB
	}
	parts := make([]string, 0, len(order))
	for _, why := range order {
		parts = append(parts, fmt.Sprintf("%d × %s", byReason[why], why))
	}
	fmt.Fprintln(w, render.Hint(fmt.Sprintf("protected: %d process(es) holding %s — %s",
		len(res.Protected), render.HumanBytes(held), strings.Join(parts, ", "))))
}

// reapViaPicker lets the user hand-select from the candidate set.
func reapViaPicker(out io.Writer, res reap.Result, cmds map[int]string) error {
	lines := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		cmdline := cmds[c.PID]
		if cmdline == "" {
			cmdline = c.Comm
		}
		lines = append(lines, fmt.Sprintf("%-7d %8s %7s %7s  %s",
			c.PID,
			render.HumanBytes(c.FootprintKiB),
			reap.FormatDuration(c.Age),
			fmt.Sprintf("%.2f%%", c.Duty()*100),
			render.ShortCommand(cmdline, 110),
		))
	}
	selected, err := runFzf(lines, fzfOptions{
		prompt: "reap > ",
		header: "PID       MEM     AGE    DUTY  COMMAND   ·   Tab=select  Enter=reap  Esc=cancel",
		multi:  true,
	})
	if err != nil {
		return err
	}
	pids := parsePickedPIDs(selected)
	if len(pids) == 0 {
		fmt.Fprintln(out, "Nothing selected.")
		return nil
	}
	proc.Terminate(pids)
	fmt.Fprintf(out, "Reaped %d process(es).\n", len(pids))
	return nil
}
