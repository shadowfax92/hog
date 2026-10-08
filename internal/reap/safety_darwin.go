//go:build darwin

package reap

/*
#cgo LDFLAGS: -framework AppKit
#include <libproc.h>
#include <stdint.h>
#include <stdlib.h>
#include <unistd.h>

// These read-only csops ABI declarations come from XNU's bsd/sys/codesign.h
// and osfmk/kern/cs_blobs.h; current public SDKs omit those headers.
extern int csops(pid_t pid, unsigned int ops, void *useraddr, size_t usersize);
#define HOG_CS_OPS_STATUS 0
#define HOG_CS_PLATFORM_BINARY 0x04000000

struct hog_identity {
	char path[PROC_PIDPATHINFO_MAXSIZE];
	int ppid;
	int platform;
	int tty;
};

static int hog_read_identity(int pid, struct hog_identity *out) {
	struct proc_bsdinfo info;
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) != sizeof(info) ||
		info.pbi_uid != getuid()) {
		return 0;
	}
	if (proc_pidpath(pid, out->path, sizeof(out->path)) <= 0) {
		return 0;
	}
	unsigned int flags = 0;
	if (csops(pid, HOG_CS_OPS_STATUS, &flags, sizeof(flags)) != 0) {
		return 0;
	}
	out->ppid = info.pbi_ppid;
	out->platform = (flags & HOG_CS_PLATFORM_BINARY) != 0;
	out->tty = (info.pbi_flags & PROC_FLAG_CONTROLT) != 0;
	return 1;
}

int hog_running_apps(int **pids, size_t *count);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
	"unsafe"

	"hog/internal/proc"
)

// InspectSafety enriches one sampled table with read-only OS observations.
// Executable paths come from the kernel, not ps's mutable display name. App
// and launchd registration are queried once for the whole table; if either
// query fails, otherwise qualifying processes are visibly protected.
func InspectSafety(procs []proc.Proc) error {
	apps, appErr := runningApps()
	jobs, jobErr := launchdJobs()
	err := errors.Join(appErr, jobErr)
	for i := range procs {
		p := &procs[i]
		p.Safety = proc.Safety{
			RunningApp:     apps[p.PID],
			LaunchdManaged: jobs[p.PID],
			Unavailable:    err != nil,
		}
		if !p.Measured {
			continue
		}
		var identity C.struct_hog_identity
		if C.hog_read_identity(C.int(p.PID), &identity) == 0 {
			p.Safety.Unavailable = true
			continue
		}
		p.Comm = C.GoString(&identity.path[0])
		p.PPID = int(identity.ppid)
		p.Safety.PlatformBinary = identity.platform != 0
		p.Safety.HasTTY = identity.tty != 0
	}
	return err
}

// runningApps copies one atomic AppKit snapshot into Go-owned storage. The
// Objective-C bridge owns and frees temporary objects; Go frees only the C
// PID buffer, after copying it. No run loop or GUI activation is needed.
func runningApps() (map[int]bool, error) {
	var pids *C.int
	var count C.size_t
	if C.hog_running_apps(&pids, &count) != 0 {
		return nil, fmt.Errorf("running application inspection failed")
	}
	defer C.free(unsafe.Pointer(pids))
	apps := make(map[int]bool, int(count))
	for _, pid := range unsafe.Slice(pids, int(count)) {
		apps[int(pid)] = true
	}
	return apps, nil
}

// launchdJobs uses launchctl's documented three-column list, rather than the
// explicitly unstable 'print' output or guessing from PPID == 1. Protecting
// every registered job is conservative and avoids per-job KeepAlive probes.
func launchdJobs() (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "/bin/launchctl", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("launchd inspection failed: %w", err)
	}
	return parseLaunchdJobs(string(raw))
}
