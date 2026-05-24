package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"hog/internal/group"
	"hog/internal/proc"
	"hog/internal/render"

	"github.com/spf13/cobra"
)

// Version is stamped at build time via -ldflags "-X hog/cmd.Version=...".
var Version = "dev"

var (
	flagDuration int
	flagMem      bool
	flagLimit    int
)

var rootCmd = &cobra.Command{
	Use:           "hog",
	Short:         "Find the apps hogging your Mac's CPU and memory",
	Long:          "hog samples running processes for a few seconds, groups them by their owning app\n(so Chrome's many helpers read as one app), and prints a color-coded table of the\nheaviest CPU and memory users. Use `hog kill <app>` to terminate a heavy group.",
	Version:       Version,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runReport,
}

func init() {
	rootCmd.Flags().IntVarP(&flagDuration, "duration", "d", 5, "sampling window in seconds (min 1)")
	rootCmd.Flags().BoolVarP(&flagMem, "mem", "m", false, "sort by memory instead of CPU")
	rootCmd.Flags().IntVarP(&flagLimit, "limit", "n", 20, "show at most N apps (0 = all)")
}

// Execute runs the root command and is the single entry point from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runReport samples the process table, folds it into app groups, and prints a
// color-coded table sorted by CPU (or memory with --mem).
func runReport(cmd *cobra.Command, _ []string) error {
	dur := flagDuration
	if dur < 1 {
		dur = 1
	}
	out := cmd.OutOrStdout()
	metric := "CPU"
	if flagMem {
		metric = "memory"
	}
	fmt.Fprintf(out, "Sampling for %ds, ranking apps by %s…\n", dur, metric)

	procs, err := proc.Sample(time.Duration(dur) * time.Second)
	if err != nil {
		return err
	}
	groups := group.Aggregate(procs)
	group.Sort(groups, flagMem)
	groups = topN(groups, flagLimit)

	fmt.Fprintln(out, render.Table(toRows(groups, runtime.NumCPU(), totalRAMBytes())))
	if len(groups) > 0 {
		fmt.Fprintln(out, render.Hint("tip: `hog details <app>` lists the processes inside an app"))
	}
	return nil
}

// toRows turns groups into render rows, coloring each cell by the app's share
// of total machine capacity (CPU share normalized by core count, memory share
// by total physical RAM).
func toRows(groups []group.Group, ncpu int, totalRAM int64) []render.Row {
	rows := make([]render.Row, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, render.Row{
			App:      g.App,
			CPUText:  fmt.Sprintf("%.0f%%", g.CPUPct),
			CPULevel: render.LevelOf(g.CPUPct / (100 * float64(ncpu))),
			MemText:  render.HumanBytes(g.RSSKiB),
			MemLevel: render.LevelOf(memShare(g.RSSKiB, totalRAM)),
			Count:    g.Count,
		})
	}
	return rows
}

// topN truncates groups to the first n. n <= 0 or n >= len means "all".
func topN(groups []group.Group, n int) []group.Group {
	if n <= 0 || n >= len(groups) {
		return groups
	}
	return groups[:n]
}

// totalRAMBytes returns physical RAM in bytes, or 0 if it can't be read (memory
// coloring then degrades to Low rather than failing the report).
func totalRAMBytes() int64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
