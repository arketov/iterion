//go:build windows

package claudesdk

import (
	"os/exec"
	"strconv"
	"syscall"

	"github.com/arketov/iterion/pkg/internal/proc"
)

// setProcessGroup is a no-op on Windows: there is no process group to set up
// at spawn; the subtree is found through parent-process links when it has to
// be killed.
func setProcessGroup(_ *exec.Cmd) {}

// terminateGroupOnCancel widens os/exec's Cancel from the leader to its
// descendants through proc.TerminateGroupOnCancel's process-tree sweep.
func terminateGroupOnCancel(cmd *exec.Cmd) { proc.TerminateGroupOnCancel(cmd) }

// killProcessGroup terminates the leader and its tree with `taskkill /F /T`.
func killProcessGroup(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	p, err := exec.LookPath("taskkill")
	if err == nil {
		// /T = tree, /F = force.
		_ = proc.Command(p, "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	}
	return nil
}
