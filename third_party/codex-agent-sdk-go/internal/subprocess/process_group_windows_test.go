//go:build windows

package subprocess

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestConfigureProcessGroup_NoConsoleWindow(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "exit 0")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("SysProcAttr = %+v, want CreationFlags == CREATE_NO_WINDOW", cmd.SysProcAttr)
	}

	// Caller-set flags and fields survive.
	kept := exec.Command("cmd", "/C", "exit 0")
	kept.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200, NoInheritHandles: true}
	configureProcessGroup(kept)
	if kept.SysProcAttr.CreationFlags != 0x00000200|createNoWindow || !kept.SysProcAttr.NoInheritHandles {
		t.Fatalf("SysProcAttr = %+v: lost a caller setting", kept.SysProcAttr)
	}

	// DETACHED_PROCESS / CREATE_NEW_CONSOLE are not mixed with CREATE_NO_WINDOW.
	for _, flag := range []uint32{0x00000008, 0x00000010} {
		c := exec.Command("cmd", "/C", "exit 0")
		c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flag}
		configureProcessGroup(c)
		if c.SysProcAttr.CreationFlags != flag {
			t.Fatalf("flags %#x became %#x", flag, c.SysProcAttr.CreationFlags)
		}
	}
}
