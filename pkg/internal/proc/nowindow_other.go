//go:build !windows

package proc

import "os/exec"

// HideWindow is a no-op off Windows: a POSIX child never gets a terminal
// window of its own, so there is nothing to suppress. See the Windows
// variant for what it does there.
func HideWindow(_ *exec.Cmd) {}

// HiddenWindowFlags is zero off Windows; see the Windows variant.
const HiddenWindowFlags uint32 = 0
