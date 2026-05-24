package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
	detailsCmd.Flags().BoolVarP(&flagDetailsKill, "kill", "k", false, "pick processes in an fzf multi-select and kill them")
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

	if flagDetailsKill {
		return killViaPicker(out, matches, byPID, cmds)
	}

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

// killViaPicker pools every process across the matched app(s), sorted by CPU
// descending (the worst offenders first), into an fzf multi-select showing
// PID/CPU/MEM/command, then terminates whatever the user picks.
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
			render.HumanBytes(p.RSSKiB),
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

// pickPIDsFzf pipes lines into `fzf --multi` and returns the PIDs of the chosen
// lines (PID is the first column). A user cancel (fzf exit 130) yields no PIDs
// and no error.
func pickPIDsFzf(lines []string) ([]int, error) {
	fzfCmd := exec.Command("fzf",
		"--multi",
		"--prompt", "kill > ",
		"--header", "PID      CPU      MEM   COMMAND   ·   Tab=select  Enter=kill  Esc=cancel",
		"--height", "100%",
		"--reverse",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, nil // user pressed Esc / Ctrl-C
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}
	return parsePickedPIDs(string(out)), nil
}

// parsePickedPIDs reads the PID (first whitespace-delimited field) from each
// non-empty line fzf echoes back.
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
func memShare(rssKiB, totalRAM int64) float64 {
	if totalRAM <= 0 {
		return 0
	}
	return float64(rssKiB) * 1024 / float64(totalRAM)
}
