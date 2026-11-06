//go:build !windows

package proc

import (
	"context"
	"os/exec"
	"testing"
)

// TestHideWindow_IsInertOffWindows: nothing about a POSIX child changes —
// SysProcAttr stays untouched, so Setpgid and friends set by other helpers
// are the only thing in it.
func TestHideWindow_IsInertOffWindows(t *testing.T) {
	cmd := exec.Command("true")
	HideWindow(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("HideWindow touched SysProcAttr off Windows: %+v", cmd.SysProcAttr)
	}
	HideWindow(nil)
	if HiddenWindowFlags != 0 {
		t.Fatalf("HiddenWindowFlags = %d off Windows, want 0", HiddenWindowFlags)
	}
}

func TestCommand_IsPlainExecOffWindows(t *testing.T) {
	for name, cmd := range map[string]*exec.Cmd{
		"Command":        Command("true"),
		"CommandContext": CommandContext(context.Background(), "true"),
	} {
		if cmd.SysProcAttr != nil {
			t.Errorf("%s set SysProcAttr off Windows: %+v", name, cmd.SysProcAttr)
		}
	}
}
