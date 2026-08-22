package health

import (
	"fmt"
	"sort"

	"hog/internal/group"
	"hog/internal/proc"
)

// maxCauses bounds how many contributors a check names. Beyond a handful the
// list stops being an explanation and becomes another table to read.
const maxCauses = 5

// appTotal accumulates one app's contribution to a check.
type appTotal struct {
	app       string
	bytes     int64
	footprint int64
	count     int
	cpu       float64
}

// byApp folds processes into their owning app, summing footprint, the portion
// of that footprint no longer resident, CPU and process count. Grouping is by
// the same key the report uses, so an app's helpers are attributed to the app
// rather than appearing as dozens of anonymous rows.
func byApp(procs []proc.Proc) map[string]*appTotal {
	totals := map[string]*appTotal{}
	for _, p := range procs {
		key := group.AppKey(p.Comm)
		if key == "" {
			continue
		}
		t := totals[key]
		if t == nil {
			t = &appTotal{app: key}
			totals[key] = t
		}
		t.footprint += p.FootprintKiB * 1024
		t.bytes += evictedBytes(p)
		t.cpu += p.CPUPct
		t.count++
	}
	return totals
}

// evictedBytes estimates how much of a process's footprint the kernel has
// pushed out of RAM, as footprint minus resident size.
//
// This is an estimate, not a reading. macOS exposes the exact compressed size
// only through task_info(TASK_VM_INFO), which needs an entitlement hog does
// not have — task_for_pid fails even for the caller's own processes. The
// subtraction is accurate for genuinely swapped processes, where nearly the
// whole footprint is gone from RAM, but overstates eviction for GPU-backed
// apps whose footprint is charged for pages that were never resident. It is
// therefore used to rank and explain, never to decide anything.
func evictedBytes(p proc.Proc) int64 {
	if v := (p.FootprintKiB - p.ResidentKiB) * 1024; v > 0 {
		return v
	}
	return 0
}

// topBy ranks apps by the chosen field and formats the leaders as causes.
func topBy(totals map[string]*appTotal, value func(*appTotal) int64, note func(*appTotal) string) []Cause {
	list := make([]*appTotal, 0, len(totals))
	for _, t := range totals {
		if value(t) > 0 {
			list = append(list, t)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if value(list[i]) != value(list[j]) {
			return value(list[i]) > value(list[j])
		}
		return list[i].app < list[j].app
	})
	if len(list) > maxCauses {
		list = list[:maxCauses]
	}
	causes := make([]Cause, 0, len(list))
	for _, t := range list {
		causes = append(causes, Cause{
			Label: t.app,
			Value: humanBytes(uint64(value(t))),
			Note:  note(t),
		})
	}
	return causes
}

// evictionCauses names the apps holding the most memory that is no longer
// resident — the pages that are in the compressor or the swap file.
func evictionCauses(procs []proc.Proc) []Cause {
	return topBy(byApp(procs),
		func(t *appTotal) int64 { return t.bytes },
		func(t *appTotal) string {
			pct := 0.0
			if t.footprint > 0 {
				pct = float64(t.bytes) / float64(t.footprint) * 100
			}
			return fmt.Sprintf("%d proc(s) · %.0f%% of its footprint evicted", t.count, pct)
		})
}

// footprintCauses names the apps holding the most memory overall.
func footprintCauses(procs []proc.Proc) []Cause {
	return topBy(byApp(procs),
		func(t *appTotal) int64 { return t.footprint },
		func(t *appTotal) string { return fmt.Sprintf("%d proc(s)", t.count) })
}

// cpuCauses names the busiest apps during the sampling window.
func cpuCauses(procs []proc.Proc) []Cause {
	totals := byApp(procs)
	list := make([]*appTotal, 0, len(totals))
	for _, t := range totals {
		if t.cpu >= 1 {
			list = append(list, t)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].cpu > list[j].cpu })
	if len(list) > maxCauses {
		list = list[:maxCauses]
	}
	causes := make([]Cause, 0, len(list))
	for _, t := range list {
		causes = append(causes, Cause{
			Label: t.app,
			Value: fmt.Sprintf("%.0f%%", t.cpu),
			Note:  fmt.Sprintf("%d proc(s)", t.count),
		})
	}
	return causes
}

// countCauses names the apps contributing the most processes, which is what
// "too many processes" actually means in practice: something is spawning and
// not reaping.
func countCauses(procs []proc.Proc) []Cause {
	totals := byApp(procs)
	list := make([]*appTotal, 0, len(totals))
	for _, t := range totals {
		list = append(list, t)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].app < list[j].app
	})
	if len(list) > maxCauses {
		list = list[:maxCauses]
	}
	causes := make([]Cause, 0, len(list))
	for _, t := range list {
		causes = append(causes, Cause{
			Label: t.app,
			Value: fmt.Sprintf("%d", t.count),
			Note:  humanBytes(uint64(t.footprint)),
		})
	}
	return causes
}
