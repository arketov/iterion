//go:build windows

package proc

import (
	"os/exec"
	"syscall"
)

const (
	// createNewConsole (CREATE_NEW_CONSOLE) asks for a console window on
	// purpose.
	createNewConsole = 0x00000010
	// detachedProcess (DETACHED_PROCESS) starts the child with no console at
	// all — and a console program started that way allocates its own, VISIBLE
	// one for each of ITS console children. It is exactly what made the
	// desktop daemon's descendants open windows.
	detachedProcess = 0x00000008
	// createNoWindow (CREATE_NO_WINDOW) runs a console program with a console
	// that has no window. Descendants inherit that console, so they open no
	// window either — even code that never went through this package.
	createNoWindow = 0x08000000
)

// HideWindow makes cmd start without a console window on Windows. It ORs
// CREATE_NO_WINDOW into SysProcAttr.CreationFlags and keeps every other flag
// (CREATE_NEW_PROCESS_GROUP and the like) and field the caller set, so it can
// run before or after them; calling it twice is harmless.
//
// A cmd that already carries DETACHED_PROCESS or CREATE_NEW_CONSOLE is left
// alone: Windows ignores CREATE_NO_WINDOW next to either, and the first is a
// deliberate "no console" while the second is a deliberate window.
//
// It does NOT set SysProcAttr.HideWindow: that is STARTF_USESHOWWINDOW /
// SW_HIDE, which would also hide the first window of a GUI program a caller
// meant to show (an editor, a browser opener).
//
// A caller that assigns cmd.SysProcAttr AFTER this call replaces the flag;
// merge into the existing struct instead (see runview and operatormcp).
func HideWindow(cmd *exec.Cmd) {
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

// HiddenWindowFlags is the CreationFlags value [HideWindow] adds, for the
// callers that build a SysProcAttr by value (a detached runner that also
// wants its own process group) instead of mutating a Cmd.
const HiddenWindowFlags uint32 = createNoWindow
