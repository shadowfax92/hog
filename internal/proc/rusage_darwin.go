//go:build darwin

package proc

/*
#include <libproc.h>
#include <sys/resource.h>
#include <mach/mach_time.h>
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"
)

// Stats is the kernel's own accounting for one process, read directly rather
// than scraped from `ps`. This exists because `ps -o rss=` is actively
// misleading on macOS: RSS counts only pages resident in RAM, so a process
// whose memory the kernel has compressed and swapped out reports ~0 KiB while
// still holding gigabytes against the machine's memory ceiling. Ranking by RSS
// therefore hides exactly the processes worth finding. Footprint is the number
// Activity Monitor shows and the number that decides whether you swap.
type Stats struct {
	Footprint     int64         // phys_footprint: resident + compressed + other charged pages
	PeakFootprint int64         // lifetime high-water mark of Footprint
	Resident      int64         // pages actually in RAM right now
	CPU           time.Duration // total user+system CPU consumed since exec
	Age           time.Duration // wall-clock time since the process started
	Threads       int           // live threads, for machine-wide accounting
}

// machTimebase converts mach absolute-time ticks to nanoseconds. The ratio is
// fixed for the life of the machine, so it is read once.
var machTimebase = sync.OnceValue(func() (r struct{ numer, denom uint64 }) {
	var tb C.mach_timebase_info_data_t
	if C.mach_timebase_info(&tb) != C.KERN_SUCCESS {
		return struct{ numer, denom uint64 }{1, 1}
	}
	return struct{ numer, denom uint64 }{uint64(tb.numer), uint64(tb.denom)}
})

// ReadStats fetches kernel accounting for one pid. ok is false when the process
// has exited or belongs to another user — callers skip it rather than failing,
// since the process table is inherently racy between enumeration and read.
func ReadStats(pid int) (Stats, bool) {
	var ri C.struct_rusage_info_v4
	if C.proc_pid_rusage(C.int(pid), C.RUSAGE_INFO_V4, (*C.rusage_info_t)(unsafe.Pointer(&ri))) != 0 {
		return Stats{}, false
	}

	tb := machTimebase()
	// Age comes from the difference of two mach absolute times, so it needs no
	// wall-clock epoch and is immune to clock adjustments.
	var age time.Duration
	if now := uint64(C.mach_absolute_time()); now > uint64(ri.ri_proc_start_abstime) {
		ticks := now - uint64(ri.ri_proc_start_abstime)
		age = time.Duration(ticks * tb.numer / tb.denom)
	}

	s := Stats{
		Footprint:     int64(ri.ri_phys_footprint),
		PeakFootprint: int64(ri.ri_lifetime_max_phys_footprint),
		CPU:           time.Duration(uint64(ri.ri_user_time) + uint64(ri.ri_system_time)),
		Age:           age,
	}

	// Resident size lives in a different struct; its absence is not fatal
	// because it only feeds the diagnostic "cold" fraction, never a kill
	// decision.
	var ti C.struct_proc_taskinfo
	if C.proc_pidinfo(C.int(pid), C.PROC_PIDTASKINFO, 0, unsafe.Pointer(&ti), C.int(unsafe.Sizeof(ti))) > 0 {
		s.Resident = int64(ti.pti_resident_size)
		s.Threads = int(ti.pti_threadnum)
	}
	return s, true
}
