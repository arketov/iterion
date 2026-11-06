//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// DetachProcessGroup is a no-op on Windows. The Unix PGID semantics
// don't have a direct analogue; the failure modes the Unix variant
// guards against (watchexec PGID-cascade kills) don't apply on
// Windows hosts.
func DetachProcessGroup(_ *exec.Cmd) {}

// TerminateGroupOnCancel makes cancellation end the command's whole process
// tree, not only the direct child. Windows has no negative-PID signal, so
// the cancel hook kills the child as os/exec's default does and then sweeps
// its descendants through their parent-process links (see killDescendants).
//
// The sweep cannot see a descendant whose intermediate parent has already
// exited (a double-fork daemon): the link is gone. A Job Object would catch
// it but needs an assignment right after Start, which os/exec has no hook
// for.
func TerminateGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Open BEFORE the kill: the handle pins the pid, and the root's
		// creation time bounds which processes count as its children. Never
		// read cmd.ProcessState here, Wait writes it concurrently.
		h, create, exit, opened := openForKill(uint32(cmd.Process.Pid))
		err := cmd.Process.Kill()
		if !opened {
			return err
		}
		defer windows.CloseHandle(h)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.EINVAL) {
			// Go's own handle is done or released, so the pid may have been
			// recycled before h was opened: sweeping could hit a stranger.
			return err
		}
		_, _ = killDescendants(treeProc{pid: uint32(cmd.Process.Pid), create: create, exit: exit, h: h})
		return err
	}
}

// KillProcessGroup terminates what the command left running after its wait
// ended: the descendants of its already-reaped root, found through
// parent-process links. Call it just after the wait. The root's pid is not
// pinned any more, so a link is accepted only when the child was created
// inside the root's lifetime, as recorded in its ProcessState. It reports
// whether anything was terminated. A descendant whose intermediate parent
// already exited is not found.
func KillProcessGroup(cmd *exec.Cmd) (bool, error) {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState == nil {
		return false, nil
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return false, nil
	}
	return killDescendants(treeProc{
		pid:    uint32(cmd.ProcessState.Pid()),
		create: fileTime(ru.CreationTime.LowDateTime, ru.CreationTime.HighDateTime),
		exit:   fileTime(ru.ExitTime.LowDateTime, ru.ExitTime.HighDateTime),
	})
}
