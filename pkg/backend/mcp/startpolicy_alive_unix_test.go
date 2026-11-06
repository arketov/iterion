//go:build unix

package mcp

import (
	"errors"
	"syscall"
	"time"
)

// processAliveWithin polls for the process to disappear, so a slow exit does
// not read as a leak. It reports whether the process is still there and, when
// it is, why we know.
//
// Only ESRCH is "gone". EPERM says the opposite — the process EXISTS and this
// uid may not signal it — and reading it as gone is how the leak this test
// guards would report as absent: a process running beside the launcher,
// reachable by nobody, is exactly the state that answers EPERM once its pid
// belongs to someone else. Any other errno is unexpected and says so rather
// than being folded into either answer.
func processAliveWithin(pid int, within time.Duration) (alive bool, why string) {
	deadline := time.Now().Add(within)
	for {
		err := syscall.Kill(pid, 0)
		switch {
		case err == nil:
			if time.Now().After(deadline) {
				return true, "signalable"
			}
		case errors.Is(err, syscall.ESRCH):
			return false, ""
		case errors.Is(err, syscall.EPERM):
			return true, "alive but owned by another uid (EPERM) — the pid was recycled, or the child outlived us"
		default:
			return true, "kill(pid, 0): " + err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
}
