//go:build windows

package runview

import "testing"

// TestNewDetachedSysProcAttr_NewGroupAndNoWindow: buildRunner assigns this
// struct over cmd.SysProcAttr, so it has to carry CREATE_NO_WINDOW itself —
// otherwise the detached runner (a console program started by a console-less
// studio) opens a console window, and each git / PowerShell / claude process
// it starts opens another.
func TestNewDetachedSysProcAttr_NewGroupAndNoWindow(t *testing.T) {
	const (
		createNewProcessGroup = 0x00000200
		createNoWindow        = 0x08000000
		detachedProcess       = 0x00000008
	)
	attr := newDetachedSysProcAttr()
	if attr.CreationFlags&createNewProcessGroup == 0 {
		t.Errorf("CreationFlags = %#x: lost CREATE_NEW_PROCESS_GROUP", attr.CreationFlags)
	}
	if attr.CreationFlags&createNoWindow == 0 {
		t.Errorf("CreationFlags = %#x: missing CREATE_NO_WINDOW", attr.CreationFlags)
	}
	if attr.CreationFlags&detachedProcess != 0 {
		t.Errorf("CreationFlags = %#x: DETACHED_PROCESS would make Windows ignore CREATE_NO_WINDOW", attr.CreationFlags)
	}
	if attr.HideWindow {
		t.Error("HideWindow (SW_HIDE) must stay unset: only CREATE_NO_WINDOW is wanted")
	}
}
