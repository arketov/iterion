//go:build !windows

package proc

import "os/exec"

// ChildWindowsEnv is the Windows no-window override; it has no effect here
// (see the Windows variant).
const ChildWindowsEnv = "ITERION_CHILD_WINDOWS"

// HideWindow is a no-op off Windows: a POSIX child never gets a terminal
// window of its own, so there is nothing to suppress. See the Windows
// variant for what it does there.
func HideWindow(_ *exec.Cmd) {}

// HiddenWindowFlags is zero off Windows; see the Windows variant.
const HiddenWindowFlags uint32 = 0

// HasConsole is always true off Windows: a POSIX process has a terminal or a
// session of its own and its children never open a window.
func HasConsole() bool { return true }

// EnsureHiddenConsole is a no-op off Windows and reports false.
func EnsureHiddenConsole() bool { return false }
