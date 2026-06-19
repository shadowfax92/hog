package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"hog/internal/group"
	"hog/internal/render"
)

type fzfOptions struct {
	prompt string
	header string
	multi  bool
}

type groupPickerMetric int

const (
	groupPickerCPU groupPickerMetric = iota
	groupPickerMem
)

type groupPickerOptions struct {
	prompt string
	action string
	metric groupPickerMetric
}

// pickGroupsFzf lets no-argument commands choose one or more app groups interactively.
func pickGroupsFzf(groups []group.Group, opts groupPickerOptions) ([]group.Group, error) {
	out, err := runFzf(groupPickerLines(groups, opts.metric), fzfOptions{
		prompt: opts.prompt,
		header: groupPickerHeader(opts.metric, opts.action),
		multi:  true,
	})
	if err != nil {
		return nil, err
	}
	return selectedGroupsByIndexes(groups, parsePickedIndexes(out)), nil
}

func groupPickerLines(groups []group.Group, metric groupPickerMetric) []string {
	lines := make([]string, 0, len(groups))
	for i, g := range groups {
		switch metric {
		case groupPickerMem:
			lines = append(lines, fmt.Sprintf("%-4d %8s %6d  %s",
				i+1,
				render.HumanBytes(g.RSSKiB),
				g.Count,
				g.App,
			))
		default:
			lines = append(lines, fmt.Sprintf("%-4d %6s %8s %6d  %s",
				i+1,
				fmt.Sprintf("%.0f%%", g.CPUPct),
				render.HumanBytes(g.RSSKiB),
				g.Count,
				g.App,
			))
		}
	}
	return lines
}

func groupPickerHeader(metric groupPickerMetric, action string) string {
	if action == "" {
		action = "choose"
	}
	switch metric {
	case groupPickerMem:
		return fmt.Sprintf("#    MEM      PROCS  APP   |   Tab=select  Enter=%s  Esc=cancel", action)
	default:
		return fmt.Sprintf("#    CPU      MEM      PROCS  APP   |   Tab=select  Enter=%s  Esc=cancel", action)
	}
}

func parsePickedIndexes(out string) []int {
	var indexes []int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		index, err := strconv.Atoi(fields[0])
		if err == nil {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func selectedGroupsByIndexes(groups []group.Group, indexes []int) []group.Group {
	selected := make([]group.Group, 0, len(indexes))
	seen := make(map[int]bool, len(indexes))
	for _, index := range indexes {
		if index < 1 || index > len(groups) || seen[index] {
			continue
		}
		seen[index] = true
		selected = append(selected, groups[index-1])
	}
	return selected
}

// runFzf shells out to fzf and treats user cancel as an empty selection.
func runFzf(lines []string, opts fzfOptions) (string, error) {
	args := make([]string, 0, 8)
	if opts.multi {
		args = append(args, "--multi")
	}
	args = append(args,
		"--prompt", opts.prompt,
		"--header", opts.header,
		"--height", "100%",
		"--reverse",
	)

	fzfCmd := exec.Command("fzf", args...)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return "", nil
		}
		return "", fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}
	return string(out), nil
}
