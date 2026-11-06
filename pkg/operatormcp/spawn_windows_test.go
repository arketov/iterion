//go:build windows

package operatormcp

import "testing"

// TestDetachedSysProcAttr_KeepsNoWindow: spawn assigns this struct over
// cmd.SysProcAttr. The old stub returned nil, which erased the CREATE_NO_WINDOW
// flag proc.Command had set and let the detached runner open a console window.
func TestDetachedSysProcAttr_KeepsNoWindow(t *testing.T) {
	attr := detachedSysProcAttr()
	if attr == nil {
		t.Fatal("detachedSysProcAttr() = nil: assigning it would drop CREATE_NO_WINDOW")
	}
	const createNoWindow = 0x08000000
	if attr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", attr.CreationFlags)
	}
}
