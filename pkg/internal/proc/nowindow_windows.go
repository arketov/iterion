//go:build windows

package proc

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const (
	// createNewConsole (CREATE_NEW_CONSOLE) asks for a console window on
	// purpose.
	createNewConsole = 0x00000010
	// detachedProcess (DETACHED_PROCESS) starts the child with no console at
	// all — and a console program started that way allocates its own, VISIBLE
	// one for each of ITS console children.
	detachedProcess = 0x00000008
	// createNoWindow (CREATE_NO_WINDOW) runs a console program with a console
	// that has no window. Descendants inherit that console, so they open no
	// window either — even code that never went through this package.
	createNoWindow = 0x08000000
)

// ChildWindowsEnv is the operator's override of the no-window default:
// "hide" starts every child windowless even from a process that has a
// console, "show" never hides one (and keeps [EnsureHiddenConsole] from
// creating a console), anything else — the default — is "auto": hide only when
// this process has no console to hand down.
const ChildWindowsEnv = "ITERION_CHILD_WINDOWS"

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcList  = kernel32.NewProc("GetConsoleProcessList")
	procAllocConsole        = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow    = kernel32.NewProc("GetConsoleWindow")
	user32                  = syscall.NewLazyDLL("user32.dll")
	procShowWindow          = user32.NewProc("ShowWindow")
	swHide                  = uintptr(0)
	hideNeeded              = defaultHideNeeded // test seam
	allocConsoleAndHideFunc = allocConsoleAndHide
)

// HasConsole reports whether this process is attached to a console, visible
// or not. A process started with CREATE_NO_WINDOW has one (without a window);
// a GUI process, or one started with DETACHED_PROCESS, has none.
func HasConsole() bool {
	var pid uint32
	n, _, _ := procGetConsoleProcList.Call(uintptr(unsafe.Pointer(&pid)), 1)
	return n != 0
}

func childWindowsMode() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(ChildWindowsEnv)))
}

// defaultHideNeeded is the auto rule: a child needs CREATE_NO_WINDOW exactly
// when this process has no console, because only then does Windows give the
// child a console window of its own. With a console (a terminal, or a hidden
// one a parent made for us) the child simply inherits it — and keeps sharing
// the terminal's Ctrl+C, which a fresh hidden console would cut it off from.
func defaultHideNeeded() bool {
	switch childWindowsMode() {
	case "hide":
		return true
	case "show":
		return false
	}
	return !HasConsole()
}

// HideWindow makes cmd start without a console window when this process has no
// console of its own (see [defaultHideNeeded] and [ChildWindowsEnv]). It ORs
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
	if cmd == nil || !hideNeeded() {
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
// wants its own process group) instead of mutating a Cmd. Those callers add it
// unconditionally: a detached runner belongs to no terminal.
const HiddenWindowFlags uint32 = createNoWindow

// EnsureHiddenConsole gives a console-less process a console with no window,
// and reports whether it made one. It is the catch-all behind [HideWindow]:
// every console program this process starts afterwards inherits that console
// and opens no window — including the ones started by code that never went
// through this package (the claw backend's bash tool, hooks and MCP stdio
// servers run inside the studio process and spawn with plain os/exec).
//
// A process that already has a console (a terminal, or the hidden one its
// parent created for it) is left alone, as is one run with
// ITERION_CHILD_WINDOWS=show. The new console window exists for the moment
// between AllocConsole and the ShowWindow(SW_HIDE) that follows it, so a
// flash of a few milliseconds is possible once at startup; per-child
// [HideWindow] stays in place for the case this fails.
func EnsureHiddenConsole() bool {
	if childWindowsMode() == "show" || HasConsole() {
		return false
	}
	return allocConsoleAndHideFunc()
}

func allocConsoleAndHide() bool {
	if r, _, _ := procAllocConsole.Call(); r == 0 {
		return false
	}
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swHide)
	}
	return true
}
