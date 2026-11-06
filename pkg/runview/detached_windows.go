//go:build windows

package runview

import (
	"syscall"

	"github.com/arketov/iterion/pkg/internal/proc"
)

// newDetachedSysProcAttr is the Windows fallback. The studio's detached
// runner mode is not validated on Windows; release.yml still cross-
// compiles a windows/{amd64,arm64} binary, so these helpers exist to
// keep the package building. A future Windows port should swap them
// for real implementations using the Win32 console-control APIs
// (GenerateConsoleCtrlEvent + OpenProcess).
//
// The runner starts in its own process group AND without a console window:
// the caller assigns this struct over cmd.SysProcAttr, so the no-window flag
// has to be part of it or the runner (a console program, started by a
// console-less studio) opens a visible console and every git/PowerShell/
// claude process it starts opens another.
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000200 | proc.HiddenWindowFlags, // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
	}
}

// terminateProcessGroup is a Windows stub. Returning errProcessNotFound
// makes callers treat the run as gone — the safest default until a
// real CTRL_BREAK_EVENT path is wired up.
func terminateProcessGroup(pid int) error {
	return errProcessNotFound
}

// pidAliveOS is a Windows stub. Same rationale as terminateProcessGroup.
func pidAliveOS(pid int) error {
	return errProcessNotFound
}
