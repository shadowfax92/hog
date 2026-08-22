package reap

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Probe exit-status convention. A probe must distinguish "I asked and the
// answer is no" from "I could not ask", because the two deserve different
// treatment: the first is a definite refusal, the second is uncertainty that
// the user's on_unknown policy resolves.
const (
	probeSafe    = 0 // safe to reap
	probeUnknown = 2 // could not determine; on_unknown decides
	// any other status means protect
)

// DefaultProbeTimeout bounds a single probe. Probes talk to live processes
// that may be wedged, and a hung probe must not hang the whole reap.
const DefaultProbeTimeout = 5 * time.Second

// probeVerdict is one probe's answer about one process.
type probeVerdict struct {
	pid     int
	protect bool
	why     string
}

// ApplyProbes asks each matching candidate whether it is safe to kill, moving
// refusals into the protected set. Probes run concurrently because each one
// forks a shell and waits on IPC with a live process; serially, a few hundred
// candidates would take minutes.
//
// A candidate matching no probe is left alone — probes can only ever protect,
// never promote something the predicates rejected.
func ApplyProbes(res *Result, probes []Probe, timeout time.Duration) {
	if len(probes) == 0 || len(res.Candidates) == 0 {
		return
	}
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}

	type job struct {
		cand  Candidate
		probe Probe
	}
	var jobs []job
	for _, c := range res.Candidates {
		if p, ok := probeFor(c.Comm, probes); ok {
			jobs = append(jobs, job{cand: c, probe: p})
		}
	}
	if len(jobs) == 0 {
		return
	}

	verdicts := make([]probeVerdict, len(jobs))
	sem := make(chan struct{}, 16) // bound concurrent shells
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			verdicts[i] = runProbe(j.cand.PID, j.probe, timeout)
		}(i, j)
	}
	wg.Wait()

	protected := make(map[int]string, len(verdicts))
	for _, v := range verdicts {
		if v.protect {
			protected[v.pid] = v.why
		}
	}
	if len(protected) == 0 {
		return
	}

	kept := res.Candidates[:0]
	for _, c := range res.Candidates {
		if why, ok := protected[c.PID]; ok {
			res.Protected = append(res.Protected, Protected{Proc: c.Proc, Why: why})
			continue
		}
		kept = append(kept, c)
	}
	res.Candidates = kept
}

// probeFor returns the first probe whose match string appears in the
// executable's basename.
func probeFor(comm string, probes []Probe) (Probe, bool) {
	base := strings.ToLower(baseName(comm))
	for _, p := range probes {
		m := strings.ToLower(strings.TrimSpace(p.Match))
		if m != "" && strings.Contains(base, m) {
			return p, true
		}
	}
	return Probe{}, false
}

// runProbe executes one probe and maps its exit status to a verdict.
func runProbe(pid int, p Probe, timeout time.Duration) probeVerdict {
	script := strings.ReplaceAll(p.Ask, "{pid}", strconv.Itoa(pid))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := exec.CommandContext(ctx, "sh", "-c", script).Run()
	label := p.Label
	if label == "" {
		label = p.Match
	}

	if err == nil {
		return probeVerdict{pid: pid, protect: false}
	}
	code := probeUnknown
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	if code == probeUnknown || ctx.Err() != nil {
		if strings.EqualFold(strings.TrimSpace(p.OnUnknown), "reap") {
			return probeVerdict{pid: pid, protect: false}
		}
		return probeVerdict{pid: pid, protect: true, why: "probe: " + label + " (could not ask)"}
	}
	return probeVerdict{pid: pid, protect: true, why: "probe: " + label}
}
