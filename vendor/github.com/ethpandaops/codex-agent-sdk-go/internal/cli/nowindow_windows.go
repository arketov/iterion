//go:build windows

package cli

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// hideWindow runs cmd without a console window (CREATE_NO_WINDOW) so the
// version probe does not flash a terminal when the host has no console. A
// process that has a console lets the probe share it. Local patch, see
// FORK.md.
func hideWindow(cmd *exec.Cmd) {
	var pid uint32

	n, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList").
		Call(uintptr(unsafe.Pointer(&pid)), 1)
	if n != 0 {
		return
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
