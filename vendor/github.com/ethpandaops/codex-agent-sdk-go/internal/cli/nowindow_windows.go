//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// hideWindow runs cmd without a console window (CREATE_NO_WINDOW) so the
// version probe does not flash a terminal when the host has no console.
// Local patch, see FORK.md.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
