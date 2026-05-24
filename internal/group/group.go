// Package group folds sampled processes into per-app groups, so an app's many
// helper processes (Chrome, Electron, etc.) read as a single owning app.
package group

import (
	"sort"
	"strings"

	"hog/internal/proc"
)

// Group is the aggregated resource usage of one app across all its processes.
type Group struct {
	App    string
	CPUPct float64
	RSSKiB int64
	Count  int
	PIDs   []int
}

// AppKey maps an executable path to its owning app name. It returns the
// outermost ".app" bundle name when present (so nested helper bundles fold into
// the top-level app), otherwise the executable's basename.
func AppKey(comm string) string {
	comm = strings.TrimSpace(comm)
	if comm == "" {
		return ""
	}
	// First ".app/" in the path is the outermost bundle.
	if i := strings.Index(comm, ".app/"); i >= 0 {
		return basename(comm[:i])
	}
	// Path that is itself a bundle with no trailing component.
	if strings.HasSuffix(comm, ".app") {
		return strings.TrimSuffix(basename(comm), ".app")
	}
	return basename(comm)
}

func basename(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Aggregate folds processes into groups keyed by AppKey, summing CPU% and RSS,
// counting processes, and collecting member PIDs. Insertion order is preserved
// for deterministic output until the caller sorts.
func Aggregate(procs []proc.Proc) []Group {
	byApp := map[string]*Group{}
	var order []string
	for _, p := range procs {
		key := AppKey(p.Comm)
		if key == "" {
			continue
		}
		g := byApp[key]
		if g == nil {
			g = &Group{App: key}
			byApp[key] = g
			order = append(order, key)
		}
		g.CPUPct += p.CPUPct
		g.RSSKiB += p.RSSKiB
		g.Count++
		g.PIDs = append(g.PIDs, p.PID)
	}
	out := make([]Group, 0, len(order))
	for _, k := range order {
		out = append(out, *byApp[k])
	}
	return out
}

// Sort orders groups in place by descending CPU% (the other metric breaks ties),
// or by descending memory when byMem is set.
func Sort(groups []Group, byMem bool) {
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if byMem {
			if a.RSSKiB != b.RSSKiB {
				return a.RSSKiB > b.RSSKiB
			}
			return a.CPUPct > b.CPUPct
		}
		if a.CPUPct != b.CPUPct {
			return a.CPUPct > b.CPUPct
		}
		return a.RSSKiB > b.RSSKiB
	})
}

// Match returns every group whose app name contains pattern, case-insensitively.
func Match(groups []Group, pattern string) []Group {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	var out []Group
	for _, g := range groups {
		if strings.Contains(strings.ToLower(g.App), pattern) {
			out = append(out, g)
		}
	}
	return out
}
