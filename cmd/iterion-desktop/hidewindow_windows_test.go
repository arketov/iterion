//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideConsoleWindow_AddsCreateNoWindowAndKeepsTheRest(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	hideConsoleWindow(cmd)
	if got, want := cmd.SysProcAttr.CreationFlags, uint32(0x00000200|createNoWindow); got != want {
		t.Fatalf("CreationFlags = %#x, want %#x", got, want)
	}

	plain := exec.Command("cmd", "/C", "exit 0")
	hideConsoleWindow(plain)
	if plain.SysProcAttr == nil || plain.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("plain cmd not hidden: %+v", plain.SysProcAttr)
	}
	if plain.SysProcAttr.HideWindow {
		t.Fatal("HideWindow (SW_HIDE) must stay unset")
	}
}

func TestHideConsoleWindow_LeavesAnExplicitConsoleChoiceAlone(t *testing.T) {
	for _, flag := range []uint32{detachedProcess, createNewConsole} {
		cmd := exec.Command("cmd", "/C", "exit 0")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flag}
		hideConsoleWindow(cmd)
		if cmd.SysProcAttr.CreationFlags != flag {
			t.Fatalf("flags %#x became %#x", flag, cmd.SysProcAttr.CreationFlags)
		}
	}
	hideConsoleWindow(nil) // must not panic
}
