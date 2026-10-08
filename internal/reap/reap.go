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

// Candidate passed the predicates or was selected through a safe tree root,
// and survived protection checks. Reasons let a dry run explain its selection.
type Candidate struct {
	proc.Proc
	Reasons []string
	ViaTree bool // pulled in as a descendant, not on its own merits
}

// Protected would otherwise have been selected by measurements or tree
// expansion, but was spared with a visible reason.
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
// Readable kernel accounting is required, but does not exclude OS components
// running as the logged-in user. Executable identity, app ownership, launchd
// registration, and the caller's ancestry supply independent protections.
// InspectSafety supplies the live OS observations; selection itself is pure so
// safety can be tested without signalling a process.
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
	protection := make(map[int]string, len(procs))
	for _, p := range procs {
		protection[p.PID] = protectionReason(p, byPID, selfLine, protectNames)
	}

	// Pass 1: processes qualifying on their own merits.
	qualified := map[int]bool{}
	for _, p := range procs {
		if !p.Measured {
			continue
		}
		if _, ok := qualifies(p, c); ok {
			qualified[p.PID] = true
		}
	}

	// Pass 2: only safe roots can pull in descendants. A protected node is a
	// lifecycle boundary: record it for display, but do not sweep through it
	// into work owned by a different app or service.
	viaTree := map[int]bool{}
	if c.Tree {
		for pid := range qualified {
			if protection[pid] != "" {
				continue
			}
			seen := map[int]bool{pid: true}
			stack := append([]int(nil), children[pid]...)
			for len(stack) > 0 {
				d := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if seen[d] {
					continue
				}
				seen[d] = true
				p := byPID[d]
				if !p.Measured {
					continue
				}
				if !qualified[d] {
					viaTree[d] = true
				}
				if protection[d] == "" {
					stack = append(stack, children[d]...)
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
		if why := protection[pid]; why != "" {
			res.Protected = append(res.Protected, Protected{Proc: p, Why: why})
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
