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
	young := dormant(2, 1, "/opt/tools/young")
	young.Age = time.Hour

	busy := dormant(3, 1, "/opt/tools/busy")
	busy.CPUTotal = 24 * time.Hour // 50% duty

	spiking := dormant(4, 1, "/opt/tools/spiking")
	spiking.CPUPct = 80

	small := dormant(5, 1, "/opt/tools/small")
	small.FootprintKiB = 1024 // 1 MiB

	procs := []proc.Proc{dormant(10, 1, "/opt/tools/reapable"), young, busy, spiking, small}
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

func TestSelectProtectsApplePaths(t *testing.T) {
	paths := []string{
		"/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock",
		"/usr/libexec/platform-helper", "/bin/platform-tool", "/sbin/platform-daemon",
		"/Library/Apple/usr/libexec/platform-helper",
		"/System/Volumes/Data/usr/libexec/platform-helper",
		"/System/Volumes/Database/platform-helper",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			res := Select([]proc.Proc{dormant(10, 1, path)}, defaultCriteria(), 999, nil)
			if len(res.Candidates) != 0 || len(res.Protected) != 1 || res.Protected[0].Why != "Apple system executable" {
				t.Fatalf("Apple executable must be visibly protected, got %+v", res)
			}
		})
	}
}

func TestSelectProtectsPlatformAppsAndServices(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		safety proc.Safety
		why    string
	}{
		{"platform outside OS paths", "/private/var/platform-helper", proc.Safety{PlatformBinary: true}, "Apple platform binary"},
		{"regular GUI app", "/Applications/Editor.app/Contents/MacOS/Editor", proc.Safety{}, "app bundle executable"},
		{"menu bar app", "/Users/me/Applications/Dictation.app/Contents/MacOS/Dictation", proc.Safety{RunningApp: true}, "app bundle executable"},
		{"reparented app helper", "/Applications/Dictation.app/Contents/Frameworks/Helper.app/Contents/MacOS/Helper", proc.Safety{}, "app bundle executable"},
		{"registered unbundled app", "/opt/tools/menu-app", proc.Safety{RunningApp: true}, "running GUI/menu-bar app"},
		{"launchd job", "/opt/tools/agent", proc.Safety{LaunchdManaged: true}, "launchd-managed service"},
		{"unavailable identity", "/opt/tools/unknown", proc.Safety{Unavailable: true}, "safety inspection unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := dormant(10, 1, c.path)
			p.Safety = c.safety
			res := Select([]proc.Proc{p}, defaultCriteria(), 999, nil)
			if len(res.Candidates) != 0 || len(res.Protected) != 1 || res.Protected[0].Why != c.why {
				t.Fatalf("want visible protection %q, got %+v", c.why, res)
			}
		})
	}
}

// Unreadable accounting is an eligibility boundary, not proof that readable
// processes are safe: logged-in-user OS components need separate protection.
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
		dormant(100, 1, "/opt/tools/terminal"),
		dormant(200, 100, "/opt/tools/shell"),
		dormant(300, 200, "/opt/tools/hog"),
		dormant(400, 1, "/opt/tools/unrelated"),
	}
	res := Select(procs, defaultCriteria(), 300, nil)
	got := pidSet(res.Candidates)

	for _, pid := range []int{100, 200, 300} {
		if got[pid] {
			t.Errorf("pid %d is self or an ancestor and must be spared", pid)
		}
	}
	if !got[400] {
		t.Error("an unrelated process should still be a candidate")
	}
	if len(res.Protected) != 3 {
		t.Errorf("self and ancestors must be visibly protected, got %+v", res.Protected)
	}
}

func TestSelectAllowsThirdPartyPaths(t *testing.T) {
	for _, path := range []string{
		"/usr/local/bin/language-server", "/opt/homebrew/bin/language-server",
		"/System/Volumes/Data/usr/local/bin/language-server",
		"/System/Volumes/Data/Users/me/bin/language-server",
		"/Users/me/app-cache.appcache/bin/helper", "/opt/tools/not-a-bundle.app",
	} {
		t.Run(path, func(t *testing.T) {
			res := Select([]proc.Proc{dormant(10, 1, path)}, defaultCriteria(), 999, nil)
			if len(res.Candidates) != 1 || len(res.Protected) != 0 {
				t.Fatalf("plain third-party CLI should remain eligible, got %+v", res)
			}
		})
	}
}

func TestSelectProtectsLaunchdHelpersAndUnknownAncestry(t *testing.T) {
	service := dormant(100, 1, "/opt/tools/service")
	service.Safety.LaunchdManaged = true
	helper := dormant(101, 100, "/opt/tools/helper")
	missing := dormant(102, 9999, "/opt/tools/unknown-owner")
	cycleA := dormant(200, 201, "/opt/tools/cycle-a")
	cycleB := dormant(201, 200, "/opt/tools/cycle-b")
	res := Select([]proc.Proc{service, helper, missing, cycleA, cycleB}, defaultCriteria(), 999, nil)
	if len(res.Candidates) != 0 || len(res.Protected) != 5 {
		t.Fatalf("services and unknown ownership must stay protected, got %+v", res)
	}
	for _, p := range res.Protected {
		if p.PID == 101 && p.Why != "launchd-owned helper" {
			t.Errorf("service helper has reason %q", p.Why)
		}
		if p.PID >= 102 && p.Why != "process ancestry unavailable" {
			t.Errorf("unknown ancestry PID %d has reason %q", p.PID, p.Why)
		}
	}
}

func TestSelectProtectNames(t *testing.T) {
	procs := []proc.Proc{dormant(30, 1, "/usr/local/bin/postgres"), dormant(31, 1, "/opt/tools/other")}
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
	parent := dormant(50, 1, "/opt/tools/editor")
	child := dormant(51, 50, "/opt/tools/languageserver")
	child.FootprintKiB = 1024 // too small to qualify alone
	grandchild := dormant(52, 51, "/opt/tools/helper")
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
	parent := dormant(60, 1, "/opt/tools/editor")
	child := dormant(61, 60, "/usr/local/bin/postgres")
	child.FootprintKiB = 1024

	crit := defaultCriteria()
	crit.Tree = true
	res := Select([]proc.Proc{parent, child}, crit, 999, []string{"postgres"})

	if pidSet(res.Candidates)[61] {
		t.Error("a protected descendant must not be swept in by --tree")
	}
}

func TestSelectProtectsAppOwnedHelpersButAllowsTerminalWork(t *testing.T) {
	app := dormant(100, 1, "/Applications/Editor.app/Contents/MacOS/Editor")
	host := dormant(101, 100, "/Applications/Editor.app/Contents/Frameworks/Helper.app/Contents/MacOS/Helper")
	server := dormant(102, 101, "/Users/me/.extensions/bin/language-server")
	worker := dormant(103, 102, "/opt/tools/worker")
	terminal := dormant(200, 1, "/Applications/Terminal.app/Contents/MacOS/Terminal")
	shell := dormant(201, 200, "/bin/zsh")
	shell.Safety.HasTTY = true
	cliServer := dormant(202, 201, "/opt/tools/language-server")
	cliServer.Safety.HasTTY = true
	// A terminal-launched tool can detach its own TTY; the shell still forms
	// the ownership boundary, so the terminal app does not own its work.
	detached := dormant(203, 201, "/usr/local/bin/language-server")

	res := Select([]proc.Proc{app, host, server, worker, terminal, shell, cliServer, detached}, defaultCriteria(), 999, nil)
	got := pidSet(res.Candidates)
	if len(got) != 2 || !got[202] || !got[203] {
		t.Fatalf("only independently launched terminal work should qualify, got %+v", res)
	}
	for _, p := range res.Protected {
		if (p.PID == 102 || p.PID == 103) && p.Why != "app-owned helper" {
			t.Errorf("helper PID %d has reason %q", p.PID, p.Why)
		}
	}
}

func TestSelectTreeCannotStartFromOrCrossProtection(t *testing.T) {
	crit := defaultCriteria()
	crit.Tree = true
	parent := dormant(100, 1, "/opt/tools/worker")
	app := dormant(101, 100, "/Applications/Example.app/Contents/MacOS/Example")
	app.FootprintKiB = 1024
	appChild := dormant(102, 101, "/opt/tools/cli")
	appChild.Safety.HasTTY = true
	appChild.Age = time.Minute
	safeChild := dormant(103, 100, "/opt/tools/helper")
	safeChild.FootprintKiB = 1024
	protectedRoot := dormant(200, 1, "/opt/tools/protected")
	rootChild := dormant(201, 200, "/opt/tools/cli")
	rootChild.Age = time.Minute

	res := Select([]proc.Proc{parent, app, appChild, safeChild, protectedRoot, rootChild}, crit, 999, []string{"protected"})
	got := pidSet(res.Candidates)
	if len(got) != 2 || !got[100] || !got[103] {
		t.Fatalf("tree must stop at protection and only start at safe roots, got %+v", res)
	}
	if len(res.Protected) != 2 {
		t.Fatalf("both blocked tree boundary and protected root must be visible, got %+v", res.Protected)
	}
}

func TestFreedSumsCandidates(t *testing.T) {
	a, b := dormant(70, 1, "/opt/tools/a"), dormant(71, 1, "/opt/tools/b")
	res := Select([]proc.Proc{a, b}, defaultCriteria(), 999, nil)
	if want := a.FootprintKiB + b.FootprintKiB; res.Freed() != want {
		t.Errorf("Freed() = %d, want %d", res.Freed(), want)
	}
}

func TestCandidatesSortedByFootprint(t *testing.T) {
	small, big := dormant(80, 1, "/opt/tools/small"), dormant(81, 1, "/opt/tools/big")
	small.FootprintKiB = 1024 * 1024
	big.FootprintKiB = 8 * 1024 * 1024

	res := Select([]proc.Proc{small, big}, defaultCriteria(), 999, nil)
	if len(res.Candidates) != 2 || res.Candidates[0].PID != 81 {
		t.Errorf("candidates should lead with the largest footprint, got %+v", res.Candidates)
	}
}
