//go:build windows

package mcp

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not
// exited (STILL_ACTIVE).
const stillActive = 259

// processAliveWithin polls for the process to disappear, so a slow exit does
// not read as a leak. It reports whether the process is still there and, when
// it is, why we know.
//
// A pid OpenProcess cannot find (ERROR_INVALID_PARAMETER) is gone. A process
// that opens but reports STILL_ACTIVE is alive. Any other error is unexpected
// and says so rather than being folded into either answer.
func processAliveWithin(pid int, within time.Duration) (alive bool, why string) {
	deadline := time.Now().Add(within)
	for {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		switch {
		case err == nil:
			var code uint32
			codeErr := windows.GetExitCodeProcess(h, &code)
			_ = windows.CloseHandle(h)
			if codeErr != nil {
				return true, "GetExitCodeProcess: " + codeErr.Error()
			}
			if code != stillActive {
				return false, ""
			}
			if time.Now().After(deadline) {
				return true, "still active"
			}
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return false, ""
		default:
			return true, "OpenProcess: " + err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
}
