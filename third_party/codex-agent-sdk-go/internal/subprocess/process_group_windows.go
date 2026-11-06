//go:build windows

package subprocess

import (
	stderrors "errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// createNoWindow is CREATE_NO_WINDOW. A host without a console of its own
// (a desktop GUI, a service) would otherwise get a visible console window
// for every codex process it starts. Local patch, see FORK.md.
const createNoWindow = 0x08000000

// hasConsole reports whether this process is attached to a console, visible or
// not (GetConsoleProcessList returns 0 only when there is none).
func hasConsole() bool {
	var pid uint32

	n, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList").
		Call(uintptr(unsafe.Pointer(&pid)), 1)

	return n != 0
}

// configureProcessGroup starts the child without a console window when this
// process has no console to hand down; a process that has one (run from a
// terminal) lets the child share it, and its Ctrl+C. It keeps
// the creation flags and SysProcAttr fields the caller already set, and
// leaves a cmd that asks for DETACHED_PROCESS (0x8) or CREATE_NEW_CONSOLE
// (0x10) alone: Windows ignores CREATE_NO_WINDOW next to either.
func configureProcessGroup(cmd *exec.Cmd) {
	if hasConsole() {
		return
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	if cmd.SysProcAttr.CreationFlags&(0x00000008|0x00000010) != 0 {
		return
	}

	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Kill(); err != nil && !stderrors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill Codex process (pid %d): %w", cmd.Process.Pid, err)
	}

	return nil
}
