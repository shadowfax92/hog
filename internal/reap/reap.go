package reap

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"hog/internal/proc"
)

// Criteria are the predicates a process must satisfy to be reapable. They are
// ANDed: a process must be old enough, dormant enough, idle right now, and big
// enough to be worth killing. No single one is sufficient — age alone would
// condemn a terminal open for a week, and dormancy alone would condemn a small
// idle daemon that costs nothing to leave running.
type Criteria struct {
	MinAge       time.Duration
	MaxDuty      float64 // fraction of lifetime spent on-CPU, e.g. 0.01 == 1%
	MaxCPU       float64 // percent CPU during the sampling window
	MinFootprint int64   // KiB
	Tree         bool    // also take descendants of qualifying processes
}

// Candidate is a process that passed every predicate, carrying the reasons it
// qualified so a dry run can explain itself.
type Candidate struct {
	proc.Proc
	Reasons []string
	ViaTree bool // pulled in as a descendant, not on its own merits
}

// Protected is a process that would otherwise have qualified but was spared,
// with the reason shown to the user so protection is never silent.
type Protected struct {
	proc.Proc
	Why string
}

// Result is one reap evaluation over the process table.
type Result struct {
	Candidates []Candidate
	Protected  []Protected
	Scanned    int // every process on the machine
	Yours      int // processes with readable kernel accounting
}

// Freed is the total footprint the candidate set would release.
func (r Result) Freed() int64 {
	var total int64
	for _, c := range r.Candidates {
		total += c.FootprintKiB
	}
	return total
}

// Select applies the predicates to a sampled process table.
//
// Two safety rules are structural rather than configurable. First, only
// processes with readable kernel accounting are eligible: macOS denies
// proc_pid_rusage for other users' processes, so this excludes every system
// daemon without hog maintaining a list of them. Second, the calling process
// and its ancestors are never candidates, so reap cannot kill the shell,
// terminal, or multiplexer it is running inside.
func Select(procs []proc.Proc, c Criteria, selfPID int, protectNames []string) Result {
	res := Result{Scanned: len(procs)}

	byPID := make(map[int]proc.Proc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
		if p.Measured {
			res.Yours++
		}
	}

	selfLine := ancestry(byPID, selfPID)

	// Pass 1: processes qualifying on their own merits.
	qualified := map[int]bool{}
	for _, p := range procs {
		if !p.Measured || selfLine[p.PID] {
			continue
		}
		if reasons, ok := qualifies(p, c); ok {
			qualified[p.PID] = true
			_ = reasons
		}
	}

	// Pass 2: with --tree, a qualifying process drags its descendants along
	// even if they are individually too small or too young. Killing a parent
	// without its children would otherwise leave orphaned helpers holding
	// memory with nothing left to serve.
	viaTree := map[int]bool{}
	if c.Tree {
		for pid := range qualified {
			for _, d := range descendants(children, pid) {
				if !qualified[d] && !selfLine[d] {
					if p, ok := byPID[d]; ok && p.Measured {
						viaTree[d] = true
					}
				}
			}
		}
	}

	// Pass 3: split the selected set into candidates and protected.
	selected := make([]int, 0, len(qualified)+len(viaTree))
	for pid := range qualified {
		selected = append(selected, pid)
	}
	for pid := range viaTree {
		selected = append(selected, pid)
	}
	sort.Ints(selected)

	for _, pid := range selected {
		p := byPID[pid]
		if name := matchesAny(p.Comm, protectNames); name != "" {
			res.Protected = append(res.Protected, Protected{Proc: p, Why: "protect: " + name})
			continue
		}
		reasons, _ := qualifies(p, c)
		if viaTree[pid] {
			reasons = append(reasons, "descendant")
		}
		res.Candidates = append(res.Candidates, Candidate{Proc: p, Reasons: reasons, ViaTree: viaTree[pid]})
	}

	sortByFootprint(res.Candidates)
	return res
}

// qualifies reports whether p passes every predicate, and describes why.
func qualifies(p proc.Proc, c Criteria) ([]string, bool) {
	var reasons []string
	if c.MinAge > 0 && p.Age < c.MinAge {
		return nil, false
	}
	if p.CPUPct > c.MaxCPU {
		return nil, false
	}
	duty := p.Duty()
	if c.MaxDuty > 0 && duty > c.MaxDuty {
		return nil, false
	}
	if p.FootprintKiB < c.MinFootprint {
		return nil, false
	}
	reasons = append(reasons, fmt.Sprintf("dormant %.2f%%", duty*100))
	if cold := p.ColdFrac(); cold > 0.5 {
		reasons = append(reasons, fmt.Sprintf("cold %.0f%%", cold*100))
	}
	return reasons, true
}

// ancestry returns the set containing pid and every ancestor up to init, so
// reap never severs the branch it is standing on.
func ancestry(byPID map[int]proc.Proc, pid int) map[int]bool {
	line := map[int]bool{}
	for pid > 1 && !line[pid] {
		line[pid] = true
		p, ok := byPID[pid]
		if !ok {
			break
		}
		pid = p.PPID
	}
	return line
}

// descendants collects the full subtree below pid, excluding pid itself.
func descendants(children map[int][]int, pid int) []int {
	var out []int
	seen := map[int]bool{pid: true}
	stack := append([]int(nil), children[pid]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		stack = append(stack, children[n]...)
	}
	return out
}

// matchesAny returns the first pattern contained in the executable's basename,
// or "" when none match.
func matchesAny(comm string, patterns []string) string {
	base := strings.ToLower(baseName(comm))
	for _, pat := range patterns {
		pat = strings.ToLower(strings.TrimSpace(pat))
		if pat != "" && strings.Contains(base, pat) {
			return pat
		}
	}
	return ""
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func sortByFootprint(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		return cs[i].FootprintKiB > cs[j].FootprintKiB
	})
}

// PIDs returns the candidate PIDs in display order.
func PIDs(cs []Candidate) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.PID
	}
	return out
}
