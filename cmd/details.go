package cmd

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	"hog/internal/group"
	"hog/internal/proc"
	"hog/internal/render"

	"github.com/spf13/cobra"
)

const commandColWidth = 90

var flagDetailsDuration int

var detailsCmd = &cobra.Command{
	Use:     "details <app>",
	Aliases: []string{"detail", "show"},
	Short:   "List the individual processes inside an app group",
	Long: "details samples the process table, finds the app(s) whose name contains <app>,\n" +
		"and lists each member process with its CPU%, memory, and full command line —\n" +
		"so you can see which of, say, node's many processes is the actual hog.",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runDetails,
}

func init() {
	detailsCmd.Flags().IntVarP(&flagDetailsDuration, "duration", "d", 5, "sampling window in seconds (min 1)")
	rootCmd.AddCommand(detailsCmd)
}

// runDetails drills into a matched app group and lists its member processes,
// sorted by CPU, each with the full command line (so same-exe processes like
// node are distinguishable by the script/args they run).
func runDetails(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	dur := flagDetailsDuration
	if dur < 1 {
		dur = 1
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sampling for %ds…\n", dur)

	procs, err := proc.Sample(time.Duration(dur) * time.Second)
	if err != nil {
		return err
	}
	matches := group.Match(group.Aggregate(procs), pattern)
	if len(matches) == 0 {
		fmt.Fprintf(out, "No running app matches %q.\n", pattern)
		return nil
	}

	byPID := make(map[int]proc.Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	cmds := proc.Commands(pidsOf(matches))
	ncpu := runtime.NumCPU()
	totalRAM := totalRAMBytes()

	for _, g := range matches {
		fmt.Fprintf(out, "\n%s — %d processes · %.0f%% CPU · %s\n",
			g.App, g.Count, g.CPUPct, render.HumanBytes(g.RSSKiB))

		pids := append([]int(nil), g.PIDs...)
		sort.SliceStable(pids, func(i, j int) bool {
			return byPID[pids[i]].CPUPct > byPID[pids[j]].CPUPct
		})

		rows := make([]render.DetailRow, 0, len(pids))
		for _, pid := range pids {
			p := byPID[pid]
			cmdline := cmds[pid]
			if cmdline == "" {
				cmdline = p.Comm
			}
			rows = append(rows, render.DetailRow{
				PID:      pid,
				CPUText:  fmt.Sprintf("%.0f%%", p.CPUPct),
				CPULevel: render.LevelOf(p.CPUPct / (100 * float64(ncpu))),
				MemText:  render.HumanBytes(p.RSSKiB),
				MemLevel: render.LevelOf(memShare(p.RSSKiB, totalRAM)),
				Command:  render.TruncateMiddle(cmdline, commandColWidth),
			})
		}
		fmt.Fprintln(out, render.DetailTable(rows))
	}
	return nil
}

// memShare is an app/process's resident memory as a fraction of physical RAM.
func memShare(rssKiB, totalRAM int64) float64 {
	if totalRAM <= 0 {
		return 0
	}
	return float64(rssKiB) * 1024 / float64(totalRAM)
}
