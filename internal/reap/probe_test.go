package reap

import (
	"testing"
	"time"

	"hog/internal/proc"
)

func probeResult(t *testing.T, script, onUnknown string) Result {
	t.Helper()
	res := Result{Candidates: []Candidate{{Proc: proc.Proc{PID: 1234, Comm: "/usr/bin/editor", FootprintKiB: 4096}}}}
	ApplyProbes(&res, []Probe{{Match: "editor", Ask: script, OnUnknown: onUnknown, Label: "unsaved work"}}, 2*time.Second)
	return res
}

// The exit-status contract is the whole probe interface, so each status is
// pinned: 0 means reap, 2 means "could not tell", anything else means protect.
func TestProbeExitStatusContract(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		onUnknown string
		reaped    bool
	}{
		{"exit 0 is safe to reap", "exit 0", "protect", true},
		{"exit 1 protects", "exit 1", "protect", false},
		{"exit 2 defers to on_unknown=protect", "exit 2", "protect", false},
		{"exit 2 defers to on_unknown=reap", "exit 2", "reap", true},
		{"unknown command protects", "definitely-not-a-real-command", "protect", false},
		{"timeout defers to on_unknown", "sleep 30", "protect", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := probeResult(t, c.script, c.onUnknown)
			if c.reaped && len(res.Candidates) != 1 {
				t.Fatalf("expected the process to stay a candidate, got %d", len(res.Candidates))
			}
			if !c.reaped && len(res.Candidates) != 0 {
				t.Fatalf("expected the process to be protected, got %d candidates", len(res.Candidates))
			}
		})
	}
}

func TestProbeSubstitutesPID(t *testing.T) {
	// The script succeeds only when {pid} was replaced with the real PID.
	res := probeResult(t, `[ "{pid}" = "1234" ]`, "protect")
	if len(res.Candidates) != 1 {
		t.Error("{pid} was not substituted with the candidate's PID")
	}
}

func TestProbeOnlyAppliesToMatchingProcesses(t *testing.T) {
	res := Result{Candidates: []Candidate{{Proc: proc.Proc{PID: 7, Comm: "/bin/unrelated"}}}}
	ApplyProbes(&res, []Probe{{Match: "editor", Ask: "exit 1"}}, time.Second)
	if len(res.Candidates) != 1 {
		t.Error("a probe must not affect processes its match does not cover")
	}
}

// Probes can only ever remove candidates; they must not resurrect a process
// the predicates already rejected.
func TestProbesNeverAddCandidates(t *testing.T) {
	res := Result{}
	ApplyProbes(&res, []Probe{{Match: "editor", Ask: "exit 0"}}, time.Second)
	if len(res.Candidates) != 0 {
		t.Error("probes must not add candidates")
	}
}

func TestProbeForMatchesBasename(t *testing.T) {
	probes := []Probe{{Match: "nvim", Ask: "exit 0"}}
	if _, ok := probeFor("/opt/homebrew/bin/nvim", probes); !ok {
		t.Error("probe should match on the executable basename")
	}
	// A path that merely contains the word must not match; only the basename counts.
	if _, ok := probeFor("/Users/me/nvim-config/bin/editor", probes); ok {
		t.Error("probe matched a directory component instead of the basename")
	}
}
