//go:build !unix

package e2e

import (
	"os/exec"
	"testing"
)

// requireUnixSignals skips: the signal-driven shutdown tests need POSIX
// signals and process groups, which this platform lacks.
func requireUnixSignals(t *testing.T) {
	t.Helper()
	t.Skip("SIGTERM handling and process groups are unix-only")
}

// The helpers below are unreachable (requireUnixSignals skips first); they
// exist only so the e2e package compiles off unix.

func signalSelfTerm() error { return nil }

func detachProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }
