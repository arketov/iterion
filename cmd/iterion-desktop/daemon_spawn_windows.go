//go:build desktop && windows

package main

import (
	"os/exec"
	"syscall"
)

// detachChild is the Windows variant of the Unix setsid trick: setting
// CREATE_NEW_PROCESS_GROUP + DETACHED_PROCESS keeps the daemon alive
// after the GUI exits and prevents ctrl-c from the GUI's console (when
// run from a terminal) from cascading to the daemon.
//
// DETACHED_PROCESS and CREATE_NO_WINDOW are mutually exclusive, so this keeps
// the former: the daemon is the GUI-subsystem desktop binary and never gets a
// console either way. Its own console children (git, hooks, the claude CLI)
// are what open windows, and they are covered where they are spawned — every
// shell-out in the server goes through pkg/internal/proc.Command, which adds
// CREATE_NO_WINDOW — not by anything this function can do for the daemon.
func detachChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= 0x00000200 // CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.CreationFlags |= 0x00000008 // DETACHED_PROCESS
}
