// Package render formats grouped resource usage into a color-coded table.
// Color reflects an app's share of total machine capacity, not raw numbers.
package render

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Level is a usage severity bucket used to pick a color.
type Level int

const (
	Low Level = iota
	Med
	High
)

// Capacity-share thresholds: an app using >30% of total CPU/RAM capacity is
// High (red), 10-30% is Med (yellow), below 10% is Low (green).
const (
	medThreshold  = 0.10
	highThreshold = 0.30
)

// LevelOf buckets a 0..1 capacity share into a severity Level.
func LevelOf(frac float64) Level {
	switch {
	case frac > highThreshold:
		return High
	case frac >= medThreshold:
		return Med
	default:
		return Low
	}
}

// Row is one rendered app line; CPU/Mem text is precomputed by the caller so
// this package stays free of machine-specific math.
type Row struct {
	App      string
	CPUText  string
	CPULevel Level
	MemText  string
	MemLevel Level
	Count    int
}

var (
	clrLow  = lipgloss.Color("10") // green
	clrMed  = lipgloss.Color("11") // yellow
	clrHigh = lipgloss.Color("9")  // red
	clrHdr  = lipgloss.Color("6")  // cyan, matches grove
)

func colorize(s string, lvl Level) string {
	c := clrLow
	switch lvl {
	case High:
		c = clrHigh
	case Med:
		c = clrMed
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// HumanBytes formats a KiB count as a compact human string (K/M/G).
func HumanBytes(kib int64) string {
	const unit = 1024.0
	switch v := float64(kib); {
	case v >= unit*unit:
		return fmt.Sprintf("%.1fG", v/(unit*unit))
	case v >= unit:
		return fmt.Sprintf("%.0fM", v/unit)
	default:
		return fmt.Sprintf("%dK", kib)
	}
}

// Table renders rows (already sorted and truncated by the caller) into a
// bordered, color-coded table. CPU and MEM cells are colored by their Level.
func Table(rows []Row) string {
	headerStyle := lipgloss.NewStyle().Foreground(clrHdr).Bold(true)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Faint(true)).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				st = headerStyle.Padding(0, 1)
			}
			if col > 0 { // CPU, MEM, PROCS are numeric — right-align
				st = st.Align(lipgloss.Right)
			}
			return st
		}).
		Headers("APP", "CPU", "MEM", "PROCS")
	for _, r := range rows {
		t.Row(r.App, colorize(r.CPUText, r.CPULevel), colorize(r.MemText, r.MemLevel), fmt.Sprintf("%d", r.Count))
	}
	return t.String()
}
