// Package render formats grouped resource usage into a color-coded table.
// Color reflects an app's share of total machine capacity, not raw numbers.
package render

import (
	"fmt"
	"strings"

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

// Size thresholds for a single process, in KiB.
const (
	medProcessSize  = 1024 * 1024     // 1 GiB
	highProcessSize = 4 * 1024 * 1024 // 4 GiB
)

// LevelOfSize buckets one process's absolute footprint. It exists alongside
// LevelOf because the two answer different questions: LevelOf measures an
// app's share of the whole machine, which is the right lens for the report but
// paints every individual process green on a large machine — a 7 GiB process
// is only 6% of 128 GiB. When the user is picking single processes to kill,
// the useful question is simply "is this one big?".
func LevelOfSize(kib int64) Level {
	switch {
	case kib >= highProcessSize:
		return High
	case kib >= medProcessSize:
		return Med
	default:
		return Low
	}
}

// ReapRow is one reap candidate. DUTY is the share of its lifetime the process
// spent on-CPU and COLD the share of its footprint the kernel has compressed
// or swapped out — together they show why the row was selected.
type ReapRow struct {
	PID      int
	MemText  string
	MemLevel Level
	AgeText  string
	DutyText string
	ColdText string
	Command  string
}

// ReapTable renders reap candidates (already sorted and truncated by the caller).
func ReapTable(rows []ReapRow) string {
	headerStyle := lipgloss.NewStyle().Foreground(clrHdr).Bold(true)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Faint(true)).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				st = headerStyle.Padding(0, 1)
			}
			if col < 5 { // PID, MEM, AGE, DUTY, COLD — right-align; COMMAND stays left
				st = st.Align(lipgloss.Right)
			}
			return st
		}).
		Headers("PID", "MEM", "AGE", "DUTY", "COLD", "COMMAND")
	for _, r := range rows {
		t.Row(
			fmt.Sprintf("%d", r.PID),
			colorize(r.MemText, r.MemLevel),
			r.AgeText,
			r.DutyText,
			lipgloss.NewStyle().Faint(true).Render(r.ColdText),
			r.Command,
		)
	}
	return t.String()
}

// ShortCommand formats a full command line for a narrow column: it reduces the
// executable to its basename and then middle-truncates the remainder. Plain
// middle truncation of the whole string hides the one field that identifies
// the process — "/Users/me/.local/share/nvim/mason/bin/rust-analyzer --log-file
// /var/folders/…/4-rust-analyzer.log" truncates to a head of anonymous path and
// a tail of log file, naming neither the program nor its job.
func ShortCommand(cmdline string, max int) string {
	exe, rest, _ := strings.Cut(strings.TrimSpace(cmdline), " ")
	if i := strings.LastIndexByte(exe, '/'); i >= 0 {
		exe = exe[i+1:]
	}
	if rest == "" {
		return TruncateMiddle(exe, max)
	}
	// Reserve the executable name in full; the arguments absorb the truncation.
	budget := max - len([]rune(exe)) - 1
	if budget < 8 {
		return TruncateMiddle(exe+" "+rest, max)
	}
	return exe + " " + TruncateMiddle(rest, budget)
}

// HealthRow is one dimension of machine health. Score is rendered as a bar so
// the shape of the problem is visible without reading every number.
type HealthRow struct {
	Name   string
	Score  int
	Level  Level
	Detail string
}

// healthBar draws a ten-cell meter. It is filled proportionally to score and
// colored by level, so a red bar that is nearly full still reads as a problem.
func healthBar(score int, lvl Level) string {
	const cells = 10
	filled := (score*cells + 50) / 100
	if filled < 0 {
		filled = 0
	}
	if filled > cells {
		filled = cells
	}
	return colorize(strings.Repeat("█", filled), lvl) +
		lipgloss.NewStyle().Faint(true).Render(strings.Repeat("·", cells-filled))
}

// HealthTable renders the per-check breakdown.
func HealthTable(rows []HealthRow) string {
	headerStyle := lipgloss.NewStyle().Foreground(clrHdr).Bold(true)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Faint(true)).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				st = headerStyle.Padding(0, 1)
			}
			if col == 1 { // SCORE is numeric
				st = st.Align(lipgloss.Right)
			}
			return st
		}).
		Headers("CHECK", "SCORE", "", "DETAIL")
	for _, r := range rows {
		t.Row(r.Name, colorize(fmt.Sprintf("%d", r.Score), r.Level), healthBar(r.Score, r.Level), r.Detail)
	}
	return t.String()
}

// Verdict renders the headline banner for `hog health`.
func Verdict(label string, score int, lvl Level) string {
	mark := "✓"
	switch lvl {
	case High:
		mark = "✗"
	case Med:
		mark = "⚠"
	}
	style := lipgloss.NewStyle().Bold(true).Foreground(levelColor(lvl))
	return style.Render(fmt.Sprintf("%s  %s — %d/100", mark, label, score))
}

func levelColor(lvl Level) lipgloss.Color {
	switch lvl {
	case High:
		return clrHigh
	case Med:
		return clrMed
	default:
		return clrLow
	}
}

// CheckHeading renders a check's name for the per-check explanation view.
func CheckHeading(name string, lvl Level) string {
	return lipgloss.NewStyle().Bold(true).Foreground(levelColor(lvl)).Render(name)
}

// causeLabelWidth is the fixed column an app name occupies in a cause line.
// Names are truncated to it so a long one cannot run into the note beside it.
const causeLabelWidth = 24

// CauseLine renders one contributor to a check: what it is, how much it
// accounts for, and a qualifying note.
func CauseLine(value, label, note string) string {
	if len([]rune(label)) > causeLabelWidth {
		label = TruncateMiddle(label, causeLabelWidth)
	}
	line := fmt.Sprintf("    %8s  %-*s", value, causeLabelWidth, label)
	if note != "" {
		return line + lipgloss.NewStyle().Faint(true).Render(note)
	}
	return line
}

// Wrap reflows text to width, prefixing each line with indent. Explanations are
// prose and need to stay readable in a narrow terminal.
func Wrap(text string, width int, indent string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	faint := lipgloss.NewStyle().Faint(true)
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, faint.Render(indent+line))
			line = w
			continue
		}
		line += " " + w
	}
	return strings.Join(append(lines, faint.Render(indent+line)), "\n")
}
