//go:build !windows

package cli

import "os/exec"

// hideWindow is a no-op off Windows: a POSIX child never opens a terminal
// window of its own.
func hideWindow(_ *exec.Cmd) {}
