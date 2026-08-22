package reap

import (
	"testing"
	"time"

	"hog/internal/proc"
)

// dormant builds a process that passes every default predicate, so each test
// can vary exactly one property and see it decide the outcome.
func dormant(pid, ppid int, comm string) proc.Proc {
	return proc.Proc{
		PID:          pid,
		PPID:         ppid,
		Comm:         comm,
		Measured:     true,
		FootprintKiB: 4 * 1024 * 1024, // 4 GiB
		Age:          48 * time.Hour,
		CPUTotal:     time.Minute, // ~0.03% duty
	}
}

func defaultCriteria() Criteria {
	return Criteria{
		MinAge:       12 * time.Hour,
		MaxDuty:      0.01, // 1%
		MaxCPU:       5,
		MinFootprint: 200 * 1024,
	}
}

func pidSet(cs []Candidate) map[int]bool {
	m := map[int]bool{}
	for _, c := range cs {
		m[c.PID] = true
	}
	return m
}

func TestSelectAppliesEveryPredicate(t *testing.T) {
	young := dormant(2, 1, "/bin/young")
	young.Age = time.Hour

	busy := dormant(3, 1, "/bin/busy")
	busy.CPUTotal = 24 * time.Hour // 50% duty

	spiking := dormant(4, 1, "/bin/spiking")
	spiking.CPUPct = 80

	small := dormant(5, 1, "/bin/small")
	small.FootprintKiB = 1024 // 1 MiB

	procs := []proc.Proc{dormant(10, 1, "/bin/reapable"), young, busy, spiking, small}
	got := pidSet(Select(procs, defaultCriteria(), 999, nil).Candidates)

	if !got[10] {
		t.Error("the dormant, old, idle, large process should be a candidate")
	}
	for _, pid := range []int{2, 3, 4, 5} {
		if got[pid] {
			t.Errorf("pid %d failed a predicate and must not be a candidate", pid)
		}
	}
}

// Processes the kernel will not report on belong to another user. Excluding
// them is what keeps reap away from system daemons without a blocklist.
func TestSelectSkipsUnmeasuredProcesses(t *testing.T) {
	system := dormant(20, 1, "/usr/libexec/somethingd")
	system.Measured = false

	res := Select([]proc.Proc{system}, defaultCriteria(), 999, nil)
	if len(res.Candidates) != 0 {
		t.Fatalf("unmeasured process was selected: %+v", res.Candidates)
	}
	if res.Yours != 0 {
		t.Errorf("Yours = %d, want 0", res.Yours)
	}
}

// reap must never kill the shell, terminal, or multiplexer it runs inside.
func TestSelectSparesSelfAndAncestors(t *testing.T) {
	procs := []proc.Proc{
		dormant(100, 1, "/bin/terminal"),
		dormant(200, 100, "/bin/shell"),
		dormant(300, 200, "/bin/hog"),
		dormant(400, 1, "/bin/unrelated"),
	}
	got := pidSet(Select(procs, defaultCriteria(), 300, nil).Candidates)

	for _, pid := range []int{100, 200, 300} {
		if got[pid] {
			t.Errorf("pid %d is self or an ancestor and must be spared", pid)
		}
	}
	if !got[400] {
		t.Error("an unrelated process should still be a candidate")
	}
}

func TestSelectProtectNames(t *testing.T) {
	procs := []proc.Proc{dormant(30, 1, "/usr/local/bin/postgres"), dormant(31, 1, "/bin/other")}
	res := Select(procs, defaultCriteria(), 999, []string{"postgres"})

	if pidSet(res.Candidates)[30] {
		t.Error("a protected name must not be a candidate")
	}
	if len(res.Protected) != 1 || res.Protected[0].PID != 30 {
		t.Fatalf("expected pid 30 in the protected set, got %+v", res.Protected)
	}
}

// --tree exists so that killing a parent does not strand its helpers, which
// would otherwise keep holding memory with nothing left to serve.
func TestSelectTreePullsInDescendants(t *testing.T) {
	parent := dormant(50, 1, "/bin/editor")
	child := dormant(51, 50, "/bin/languageserver")
	child.FootprintKiB = 1024 // too small to qualify alone
	grandchild := dormant(52, 51, "/bin/helper")
	grandchild.Age = time.Minute // too young to qualify alone

	crit := defaultCriteria()
	if got := pidSet(Select([]proc.Proc{parent, child, grandchild}, crit, 999, nil).Candidates); got[51] || got[52] {
		t.Error("without --tree, descendants failing predicates must be left alone")
	}

	crit.Tree = true
	res := Select([]proc.Proc{parent, child, grandchild}, crit, 999, nil)
	got := pidSet(res.Candidates)
	for _, pid := range []int{50, 51, 52} {
		if !got[pid] {
			t.Errorf("with --tree, pid %d should be included", pid)
		}
	}
}

// A protected descendant stays protected even when --tree sweeps its parent.
func TestSelectTreeRespectsProtection(t *testing.T) {
	parent := dormant(60, 1, "/bin/editor")
	child := dormant(61, 60, "/usr/local/bin/postgres")
	child.FootprintKiB = 1024

	crit := defaultCriteria()
	crit.Tree = true
	res := Select([]proc.Proc{parent, child}, crit, 999, []string{"postgres"})

	if pidSet(res.Candidates)[61] {
		t.Error("a protected descendant must not be swept in by --tree")
	}
}

func TestFreedSumsCandidates(t *testing.T) {
	a, b := dormant(70, 1, "/bin/a"), dormant(71, 1, "/bin/b")
	res := Select([]proc.Proc{a, b}, defaultCriteria(), 999, nil)
	if want := a.FootprintKiB + b.FootprintKiB; res.Freed() != want {
		t.Errorf("Freed() = %d, want %d", res.Freed(), want)
	}
}

func TestCandidatesSortedByFootprint(t *testing.T) {
	small, big := dormant(80, 1, "/bin/small"), dormant(81, 1, "/bin/big")
	small.FootprintKiB = 1024 * 1024
	big.FootprintKiB = 8 * 1024 * 1024

	res := Select([]proc.Proc{small, big}, defaultCriteria(), 999, nil)
	if len(res.Candidates) != 2 || res.Candidates[0].PID != 81 {
		t.Errorf("candidates should lead with the largest footprint, got %+v", res.Candidates)
	}
}
