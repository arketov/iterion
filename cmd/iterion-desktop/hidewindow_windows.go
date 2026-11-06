//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	createNewConsole = 0x00000010 // CREATE_NEW_CONSOLE
	detachedProcess  = 0x00000008 // DETACHED_PROCESS
	createNoWindow   = 0x08000000 // CREATE_NO_WINDOW
)

// hideConsoleWindow starts cmd without a console window. The desktop is a GUI
// process with no console of its own, so a console program it launches
// (claude/codex/git --version probes) would otherwise flash a terminal.
// It mirrors pkg/internal/proc.HideWindow, which cmd/ cannot import: ORs
// CREATE_NO_WINDOW into the creation flags, keeps the rest, and leaves a cmd
// that already asks for DETACHED_PROCESS or CREATE_NEW_CONSOLE alone.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if cmd.SysProcAttr.CreationFlags&(detachedProcess|createNewConsole) != 0 {
		return
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
