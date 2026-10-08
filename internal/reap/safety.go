package reap

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"hog/internal/proc"
)

// protectionReason combines direct identity with inherited ownership before
// user-configured rules. Native inspection supplies facts; this policy remains
// pure so an unsafe selection can be reproduced with synthetic process tables.
func protectionReason(p proc.Proc, byPID map[int]proc.Proc, selfLine map[int]bool, names []string) string {
	if selfLine[p.PID] {
		return "hog or ancestor"
	}
	if why := directProtection(p); why != "" {
		return why
	}
	if why := helperProtection(p, byPID); why != "" {
		return why
	}
	if name := matchesAny(p.Comm, names); name != "" {
		return "protect: " + name
	}
	return ""
}

// helperProtection follows ownership even when an app launches an executable
// outside its bundle. A controlling terminal breaks inheritance, so a shell
// and its CLI work remain independent of the terminal app. Live GUI-editor
// language servers without that boundary are deliberately kept with the app.
func helperProtection(p proc.Proc, byPID map[int]proc.Proc) string {
	seen := map[int]bool{p.PID: true}
	for !p.Safety.HasTTY && p.PPID > 1 {
		if seen[p.PPID] {
			return "process ancestry unavailable"
		}
		seen[p.PPID] = true
		parent, ok := byPID[p.PPID]
		if !ok {
			return "process ancestry unavailable"
		}
		if appExecutable(executablePath(parent)) || parent.Safety.RunningApp {
			return "app-owned helper"
		}
		if parent.Safety.HasTTY {
			return ""
		}
		switch {
		case parent.Safety.LaunchdManaged:
			return "launchd-owned helper"
		case parent.Safety.Unavailable || !filepath.IsAbs(executablePath(parent)):
			return "safety inspection unavailable"
		case parent.PPID == 1:
			return "launchd/orphan ancestry"
		}
		p = parent
	}
	return ""
}

// directProtection uses executable identity and OS registration, never names.
// Its precedence gives each spared process one stable, useful display reason.
func directProtection(p proc.Proc) string {
	path := executablePath(p)
	switch {
	case appleExecutable(path):
		return "Apple system executable"
	case p.Safety.PlatformBinary:
		return "Apple platform binary"
	case appExecutable(path):
		return "app bundle executable"
	case p.Safety.RunningApp:
		return "running GUI/menu-bar app"
	case p.Safety.LaunchdManaged:
		return "launchd-managed service"
	case p.Safety.Unavailable || !filepath.IsAbs(path):
		return "safety inspection unavailable"
	case p.PPID == 1:
		// The current bootstrap registry cannot disprove ownership by a job in
		// another domain. PID 1 also adopts orphaned app helpers, so both cases
		// must be spared rather than treating absence from 'list' as permission.
		return "launchd child or orphan"
	default:
		return ""
	}
}

func executablePath(p proc.Proc) string {
	if p.Safety.Executable != "" {
		return p.Safety.Executable
	}
	return p.Comm
}

// appExecutable covers every executable in a bundle, including nested helper
// apps and XPC services that launchd may reparent away from the app's main PID.
func appExecutable(path string) bool {
	parts := strings.Split(strings.ToLower(filepath.Clean(path)), "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.HasSuffix(part, ".app") {
			return true
		}
	}
	return false
}

// appleExecutable recognizes OS-owned directories, not program names. /usr/local
// is deliberately excluded: third-party CLI tools live there on Intel Macs.
func appleExecutable(path string) bool {
	path = strings.ToLower(filepath.Clean(path))
	// APFS can expose the same executable through its Data-volume path. Strip
	// that alias first so /System/Volumes/Data/Users is not mistaken for the OS.
	if inside(path, "/system/volumes/data") {
		path = strings.TrimPrefix(path, "/system/volumes/data")
	}
	if inside(path, "/usr/local") {
		return false
	}
	for _, root := range []string{"/system", "/usr", "/bin", "/sbin", "/library/apple"} {
		if inside(path, root) {
			return true
		}
	}
	return false
}

func inside(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// parseLaunchdJobs rejects incomplete or unexpected output so a failed query
// cannot masquerade as an empty registry and make services reapable.
func parseLaunchdJobs(raw string) (map[int]bool, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if strings.Join(strings.Fields(lines[0]), " ") != "PID Status Label" {
		return nil, fmt.Errorf("launchd inspection returned an unexpected table")
	}
	jobs := map[int]bool{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("launchd inspection returned an incomplete row")
		}
		if _, err := strconv.Atoi(fields[1]); err != nil {
			return nil, fmt.Errorf("launchd inspection returned an invalid status")
		}
		if fields[0] == "-" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("launchd inspection returned an invalid PID")
		}
		jobs[pid] = true
	}
	return jobs, nil
}
