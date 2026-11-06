//go:build windows

package dispatcher

import (
	"math"

	"golang.org/x/sys/windows"
)

// localPidGone reports whether the given local PID is definitively gone,
// i.e. a claim marker referencing it is safe to reclaim. Same contract as
// the Unix twin: true only when the process provably does not run, false on
// any doubt, so the sweep never reclaims a claim from a live daemon.
//
// os.Process.Signal cannot probe on Windows, so ask the OS: no such pid
// (ERROR_INVALID_PARAMETER) is gone; any other OpenProcess error (access
// denied to another user's process) proves the pid exists; an openable
// process object is signalled once the process has exited — the exit code
// cannot tell, STILL_ACTIVE is also a code a process may exit with.
//
// Windows reuses pids quickly, so a dead daemon's pid may belong to an
// unrelated process and read as live: the claim then stays, the safe
// direction, and the pid alone cannot say more.
func localPidGone(pid int) bool {
	if pid <= 0 || int64(pid) > math.MaxUint32 {
		// uint32(pid) would wrap and probe a different process.
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == windows.WAIT_OBJECT_0
}
