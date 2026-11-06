//go:build !windows

package main

import "os/exec"

// hideConsoleWindow is a no-op off Windows: a POSIX child never opens a
// terminal window of its own.
func hideConsoleWindow(_ *exec.Cmd) {}
