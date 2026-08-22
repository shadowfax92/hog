package cmd

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"hog/internal/group"
	"hog/internal/proc"
	"hog/internal/render"

	"github.com/spf13/cobra"
)

const commandColWidth = 90

var (
	flagDetailsDuration int
	flagDetailsKill     bool
)

var detailsCmd = &cobra.Command{
	Use:     "details [app]",
	Aliases: []string{"detail", "show"},
	Short:   "List the individual processes inside an app group",
	Long: "details samples the process table, finds the app(s) whose name contains <app>,\n" +
		"and lists each member process with its CPU%, memory, and full command line —\n" +
		"so you can see which of, say, node's many processes is the actual hog.\n" +
		"If <app> is omitted, details opens an fzf multi-select picker of app groups.",
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runDetails,
}

func init() {
	detailsCmd.Flags().IntVarP(&flagDetailsDuration, "duration", "d", 5, "sampling window in seconds (min 1)")
	detailsCmd.Flags().BoolVarP(&flagDetailsKill, "kill", "k", false, "pick processes in an fzf multi-select and kill them")
	rootCmd.AddCommand(detailsCmd)
}

// runDetails lists the sampled processes inside one or more selected app groups.
func runDetails(cmd *cobra.Command, args []string) error {
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
	groups := group.Aggregate(procs)
	matches, err := detailsGroups(out, groups, args)
	if err != nil || len(matches) == 0 {
		return err
	}

	byPID := make(map[int]proc.Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	cmds := proc.Commands(pidsOf(matches))

	if flagDetailsKill {
		return killViaPicker(out, matches, byPID, cmds)
	}

	ncpu := runtime.NumCPU()
	totalRAM := totalRAMBytes()

	for _, g := range matches {
		fmt.Fprintf(out, "\n%s — %d processes · %.0f%% CPU · %s\n",
			g.App, g.Count, g.CPUPct, render.HumanBytes(g.FootprintKiB))

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
				MemText:  render.HumanBytes(p.FootprintKiB),
				MemLevel: render.LevelOf(memShare(p.FootprintKiB, totalRAM)),
				Command:  render.TruncateMiddle(cmdline, commandColWidth),
			})
		}
		fmt.Fprintln(out, render.DetailTable(rows))
	}
	return nil
}

// detailsGroups matches an app arg or asks fzf to choose groups when the arg is omitted.
func detailsGroups(out io.Writer, groups []group.Group, args []string) ([]group.Group, error) {
	if len(args) > 0 {
		pattern := args[0]
		matches := group.Match(groups, pattern)
		if len(matches) == 0 {
			fmt.Fprintf(out, "No running app matches %q.\n", pattern)
		}
		return matches, nil
	}

	group.Sort(groups, false)
	if len(groups) == 0 {
		fmt.Fprintln(out, "No running apps found.")
		return nil, nil
	}

	action := "details"
	if flagDetailsKill {
		action = "processes"
	}
	matches, err := pickGroupsFzf(groups, groupPickerOptions{
		prompt: "apps > ",
		action: action,
		metric: groupPickerCPU,
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		fmt.Fprintln(out, "Nothing selected.")
	}
	return matches, nil
}

// killViaPicker lets the user pick individual processes from matched groups.
func killViaPicker(out io.Writer, matches []group.Group, byPID map[int]proc.Proc, cmds map[int]string) error {
	pids := pidsOf(matches)
	sort.SliceStable(pids, func(i, j int) bool {
		return byPID[pids[i]].CPUPct > byPID[pids[j]].CPUPct
	})

	lines := make([]string, 0, len(pids))
	for _, pid := range pids {
		p := byPID[pid]
		cmdline := cmds[pid]
		if cmdline == "" {
			cmdline = p.Comm
		}
		lines = append(lines, fmt.Sprintf("%-7d %6s %8s  %s",
			pid,
			fmt.Sprintf("%.0f%%", p.CPUPct),
			render.HumanBytes(p.FootprintKiB),
			render.TruncateMiddle(cmdline, 120),
		))
	}

	selected, err := pickPIDsFzf(lines)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Fprintln(out, "Nothing selected.")
		return nil
	}

	proc.Terminate(selected)
	ids := make([]string, len(selected))
	for i, p := range selected {
		ids[i] = strconv.Itoa(p)
	}
	fmt.Fprintf(out, "Terminated %d process(es): %s\n", len(selected), strings.Join(ids, " "))
	return nil
}

// pickPIDsFzf returns the first-column PIDs from the selected fzf lines.
func pickPIDsFzf(lines []string) ([]int, error) {
	out, err := runFzf(lines, fzfOptions{
		prompt: "kill > ",
		header: "PID      CPU      MEM   COMMAND   ·   Tab=select  Enter=kill  Esc=cancel",
		multi:  true,
	})
	if err != nil {
		return nil, err
	}
	return parsePickedPIDs(out), nil
}

// parsePickedPIDs reads the first whitespace-delimited field from fzf output.
func parsePickedPIDs(out string) []int {
	var pids []int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// memShare is an app/process's resident memory as a fraction of physical RAM.
func memShare(footprintKiB, totalRAM int64) float64 {
	if totalRAM <= 0 {
		return 0
	}
	return float64(footprintKiB) * 1024 / float64(totalRAM)
}
