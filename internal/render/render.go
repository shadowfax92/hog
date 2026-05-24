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

// DetailRow is one process line inside an app group (for `hog details`).
type DetailRow struct {
	PID      int
	CPUText  string
	CPULevel Level
	MemText  string
	MemLevel Level
	Command  string
}

// TruncateMiddle shortens s to at most max runes, keeping the head and tail and
// dropping the middle (where a "…" goes). Middle truncation keeps both the
// executable hint and the tail of a command (script names live at the end).
func TruncateMiddle(s string, max int) string {
	r := []rune(s)
	if max < 1 || len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	avail := max - 1 // one rune for the ellipsis
	head := avail / 2
	tail := avail - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// DetailTable renders per-process rows (already sorted/truncated by the caller).
func DetailTable(rows []DetailRow) string {
	headerStyle := lipgloss.NewStyle().Foreground(clrHdr).Bold(true)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Faint(true)).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				st = headerStyle.Padding(0, 1)
			}
			if col < 3 { // PID, CPU, MEM — right-align; COMMAND stays left
				st = st.Align(lipgloss.Right)
			}
			return st
		}).
		Headers("PID", "CPU", "MEM", "COMMAND")
	for _, r := range rows {
		t.Row(fmt.Sprintf("%d", r.PID), colorize(r.CPUText, r.CPULevel), colorize(r.MemText, r.MemLevel), r.Command)
	}
	return t.String()
}

// Hint renders an unobtrusive faint tip line.
func Hint(text string) string {
	return lipgloss.NewStyle().Faint(true).Render(text)
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
