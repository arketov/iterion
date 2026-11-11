//go:build unix

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// requireUnixSignals: signal-driven shutdown tests run on unix.
func requireUnixSignals(t *testing.T) { t.Helper() }

// signalSelfTerm delivers SIGTERM to the test process itself, the way a
// supervisor stops the dispatch daemon that runs in-process.
func signalSelfTerm() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}

// detachProcessGroup makes cmd the leader of its own process group, so a
// SIGINT to the parent test process does not cascade into it.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup SIGKILLs the group detachProcessGroup created for cmd,
// returning os.ErrProcessDone when it is already gone.
func killProcessGroup(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
