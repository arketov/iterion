//go:build windows

package operatormcp

import (
	"fmt"
	"syscall"

	"github.com/arketov/iterion/pkg/internal/proc"
)

// detachedSysProcAttr is a Windows stub for the session/process-group
// detachment (mirrors runview's detached_windows.go — detached-run support
// is Unix-first). It still carries CREATE_NO_WINDOW: the caller assigns this
// struct over cmd.SysProcAttr, so returning nil would drop the no-window flag
// proc.Command set and the runner (and every console program it starts)
// would open a console window.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: proc.HiddenWindowFlags}
}

// terminateProcessGroup is unsupported on Windows.
func terminateProcessGroup(pid int) error {
	return fmt.Errorf("operatormcp: cancelling a detached run is not supported on windows (pid %d)", pid)
}

// pidAlive conservatively reports "not found" on Windows, matching the
// runview stub: liveness probing is Unix-only.
func pidAlive(pid int) error { return errProcessNotFound }
