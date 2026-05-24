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
	Use:   "kill <app>",
	Short: "Terminate every process belonging to a matching app",
	Long: "kill snapshots the process table once, finds apps whose name contains <app>\n" +
		"(case-insensitive), shows what it will terminate, then sends SIGTERM and\n" +
		"escalates to SIGKILL for survivors. Use -f to skip the confirmation prompt.",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runKill,
}

func init() {
	killCmd.Flags().BoolVarP(&flagKillForce, "force", "f", false, "skip the confirmation prompt")
	rootCmd.AddCommand(killCmd)
}

func runKill(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	procs, err := proc.List()
	if err != nil {
		return err
	}
	matches := group.Match(group.Aggregate(procs), pattern)
	out := cmd.OutOrStdout()
	if len(matches) == 0 {
		fmt.Fprintf(out, "No running app matches %q.\n", pattern)
		return nil
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

// confirm prints prompt and returns true only for an affirmative y/yes line;
// anything else (including a bare Enter) is treated as No.
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
