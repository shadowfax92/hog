package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"hog/internal/group"
	"hog/internal/proc"

	"github.com/spf13/cobra"
)

var flagKillForce bool

var killCmd = &cobra.Command{
	Use:   "kill [app]",
	Short: "Terminate every process belonging to a matching app",
	Long: "kill snapshots the process table once, finds apps whose name contains <app>\n" +
		"(case-insensitive), shows what it will terminate, then sends SIGTERM and\n" +
		"escalates to SIGKILL for survivors. Use -f to skip the confirmation prompt.\n" +
		"If <app> is omitted, kill opens an fzf multi-select picker of app groups.",
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runKill,
}

func init() {
	killCmd.Flags().BoolVarP(&flagKillForce, "force", "f", false, "skip the confirmation prompt")
	rootCmd.AddCommand(killCmd)
}

// runKill terminates all processes in matched or interactively selected app groups.
func runKill(cmd *cobra.Command, args []string) error {
	procs, err := proc.List()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	groups := group.Aggregate(procs)
	matches, err := killGroups(out, groups, args)
	if err != nil || len(matches) == 0 {
		return err
	}

	pids := pidsOf(matches)
	fmt.Fprintf(out, "Will terminate %d process(es) across: %s\n", len(pids), strings.Join(appLabels(matches), ", "))
	if !flagKillForce && !confirm(cmd.InOrStdin(), out, "Proceed? [y/N] ") {
		fmt.Fprintln(out, "Aborted.")
		return nil
	}

	proc.Terminate(pids)
	fmt.Fprintf(out, "Sent termination to %d process(es).\n", len(pids))
	return nil
}

// killGroups matches an app arg or asks fzf to choose groups when the arg is omitted.
func killGroups(out io.Writer, groups []group.Group, args []string) ([]group.Group, error) {
	if len(args) > 0 {
		pattern := args[0]
		matches := group.Match(groups, pattern)
		if len(matches) == 0 {
			fmt.Fprintf(out, "No running app matches %q.\n", pattern)
		}
		return matches, nil
	}

	group.Sort(groups, true)
	if len(groups) == 0 {
		fmt.Fprintln(out, "No running apps found.")
		return nil, nil
	}

	matches, err := pickGroupsFzf(groups, groupPickerOptions{
		prompt: "kill > ",
		action: "kill",
		metric: groupPickerMem,
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		fmt.Fprintln(out, "Nothing selected.")
	}
	return matches, nil
}

// pidsOf returns the union of all PIDs across the matched groups, in order.
func pidsOf(groups []group.Group) []int {
	var pids []int
	for _, g := range groups {
		pids = append(pids, g.PIDs...)
	}
	return pids
}

func appLabels(groups []group.Group) []string {
	labels := make([]string, len(groups))
	for i, g := range groups {
		labels[i] = fmt.Sprintf("%s (%d)", g.App, g.Count)
	}
	return labels
}

// confirm returns true only for an affirmative y/yes response.
func confirm(r io.Reader, w io.Writer, prompt string) bool {
	fmt.Fprint(w, prompt)
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
