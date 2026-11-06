package proc

import (
	"context"
	"os/exec"
)

// Command is [exec.Command] whose child never gets a console window of its
// own on Windows; on every other OS it IS [exec.Command].
//
// Why a seam instead of a flag at each call site: the studio runs without a
// console on Windows (the desktop app is a GUI process, and the daemon it
// spawns has none either), and every console program such a process starts
// — git, a PowerShell hook, the claude CLI, node, an MCP stdio server —
// otherwise opens a console window of its own that flashes and closes. A run
// that shells out dozens of times a minute is a burst of terminals. Windows
// gives a child its own window only when it is a console program and the
// parent has no console to hand down; CREATE_NO_WINDOW (see [HideWindow])
// is the one flag that stops it, and it has to be on the CreateProcess call
// of every such child. Routing the construction through here makes the flag
// the default and a raw `exec.Command` in production code the exception —
// TestNoRawExecCommand pins that, so a new shell-out cannot quietly bring
// the terminals back.
//
// Pipes, stdio redirection, exit codes, the working directory, the
// environment and context cancellation are exactly os/exec's: the wrapper
// touches nothing but SysProcAttr.CreationFlags.
func Command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	HideWindow(cmd)
	return cmd
}

// CommandContext is [exec.CommandContext] with the same no-window default as
// [Command].
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	HideWindow(cmd)
	return cmd
}
