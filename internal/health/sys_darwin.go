//go:build darwin

package health

/*
#include <mach/mach.h>
#include <mach/mach_host.h>
#include <sys/sysctl.h>
#include <sys/types.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

// VMStat is one reading of the kernel's virtual-memory counters. Page counts
// are instantaneous; the swap and page counters are cumulative since boot and
// only mean anything when two readings are differenced over a known interval.
type VMStat struct {
	PageSize                                              uint64
	Free, Active, Inactive, Wired, Speculative, Purgeable uint64 // pages
	Compressor                                            uint64 // pages held by the compressor
	Swapins, Swapouts, Pageins, Pageouts, Compressions    uint64 // cumulative
	Decompressions                                        uint64 // cumulative
}

// CPUTicks is the cumulative time all cores have spent in each state. As with
// VMStat's counters, only the difference between two readings is meaningful:
// the absolute values cover the machine's entire uptime.
type CPUTicks struct{ User, System, Idle, Nice uint64 }

// Total is the tick sum, the denominator for any per-state share.
func (c CPUTicks) Total() uint64 { return c.User + c.System + c.Idle + c.Nice }

// Sub returns the ticks accumulated between an earlier reading and this one.
func (c CPUTicks) Sub(prev CPUTicks) CPUTicks {
	return CPUTicks{
		User: satSub(c.User, prev.User), System: satSub(c.System, prev.System),
		Idle: satSub(c.Idle, prev.Idle), Nice: satSub(c.Nice, prev.Nice),
	}
}

// satSub subtracts without wrapping. Kernel counters can appear to move
// backwards across a reading (they are not sampled atomically), and an
// unsigned wrap would turn a tiny discrepancy into an astronomical rate.
func satSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// SwapUsage is the backing store's size and occupancy, in bytes.
type SwapUsage struct{ Total, Used, Avail uint64 }

// ReadVM reads the host's virtual-memory statistics.
func ReadVM() (VMStat, error) {
	var info C.vm_statistics64_data_t
	count := C.mach_msg_type_number_t(C.HOST_VM_INFO64_COUNT)
	if rc := C.host_statistics64(C.host_t(C.mach_host_self()), C.HOST_VM_INFO64,
		C.host_info64_t(unsafe.Pointer(&info)), &count); rc != C.KERN_SUCCESS {
		return VMStat{}, fmt.Errorf("host_statistics64: mach error %d", int(rc))
	}
	var pageSize C.vm_size_t
	if C.host_page_size(C.host_t(C.mach_host_self()), &pageSize) != C.KERN_SUCCESS {
		pageSize = 4096
	}
	return VMStat{
		PageSize:       uint64(pageSize),
		Free:           uint64(info.free_count),
		Active:         uint64(info.active_count),
		Inactive:       uint64(info.inactive_count),
		Wired:          uint64(info.wire_count),
		Speculative:    uint64(info.speculative_count),
		Purgeable:      uint64(info.purgeable_count),
		Compressor:     uint64(info.compressor_page_count),
		Swapins:        uint64(info.swapins),
		Swapouts:       uint64(info.swapouts),
		Pageins:        uint64(info.pageins),
		Pageouts:       uint64(info.pageouts),
		Compressions:   uint64(info.compressions),
		Decompressions: uint64(info.decompressions),
	}, nil
}

// ReadCPU reads cumulative per-state CPU ticks summed across all cores.
func ReadCPU() (CPUTicks, error) {
	var info C.host_cpu_load_info_data_t
	count := C.mach_msg_type_number_t(C.HOST_CPU_LOAD_INFO_COUNT)
	if rc := C.host_statistics(C.host_t(C.mach_host_self()), C.HOST_CPU_LOAD_INFO,
		C.host_info_t(unsafe.Pointer(&info)), &count); rc != C.KERN_SUCCESS {
		return CPUTicks{}, fmt.Errorf("host_statistics: mach error %d", int(rc))
	}
	return CPUTicks{
		User:   uint64(info.cpu_ticks[C.CPU_STATE_USER]),
		System: uint64(info.cpu_ticks[C.CPU_STATE_SYSTEM]),
		Idle:   uint64(info.cpu_ticks[C.CPU_STATE_IDLE]),
		Nice:   uint64(info.cpu_ticks[C.CPU_STATE_NICE]),
	}, nil
}

// ReadSwap reports swap-file size and occupancy. A total of zero means macOS
// has not needed to create a swap file at all, which is the healthy state.
func ReadSwap() (SwapUsage, error) {
	var xsu C.struct_xsw_usage
	size := C.size_t(unsafe.Sizeof(xsu))
	name := C.CString("vm.swapusage")
	defer C.free(unsafe.Pointer(name))
	if C.sysctlbyname(name, unsafe.Pointer(&xsu), &size, nil, 0) != 0 {
		return SwapUsage{}, fmt.Errorf("sysctl vm.swapusage")
	}
	return SwapUsage{Total: uint64(xsu.xsu_total), Used: uint64(xsu.xsu_used), Avail: uint64(xsu.xsu_avail)}, nil
}

// ReadLoadAvg returns the 1, 5, and 15 minute load averages.
func ReadLoadAvg() [3]float64 {
	var avg [3]C.double
	var out [3]float64
	if n := C.getloadavg(&avg[0], 3); n == 3 {
		for i := range out {
			out[i] = float64(avg[i])
		}
	}
	return out
}

// Memory-pressure levels as reported by kern.memorystatus_vm_pressure_level.
const (
	PressureNormal   = 1
	PressureWarning  = 2
	PressureCritical = 4
)

// ReadPressureLevel returns the kernel's own memory-pressure verdict, which is
// worth more than any threshold hog could invent: it is the same signal macOS
// uses to decide when to start terminating applications.
func ReadPressureLevel() int {
	var level C.int
	size := C.size_t(unsafe.Sizeof(level))
	name := C.CString("kern.memorystatus_vm_pressure_level")
	defer C.free(unsafe.Pointer(name))
	if C.sysctlbyname(name, unsafe.Pointer(&level), &size, nil, 0) != 0 {
		return PressureNormal
	}
	return int(level)
}

// DiskUsage is free and total bytes on a filesystem.
type DiskUsage struct{ Free, Total uint64 }

// ReadDisk reports space on the volume containing path.
func ReadDisk(path string) (DiskUsage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskUsage{}, err
	}
	return DiskUsage{Free: uint64(st.Bavail) * uint64(st.Bsize), Total: uint64(st.Blocks) * uint64(st.Bsize)}, nil
}
