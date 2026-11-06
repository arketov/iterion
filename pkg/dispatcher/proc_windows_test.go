//go:build windows

package dispatcher

import (
	"math"
	"os"
	"os/exec"
	"testing"
)

// TestLocalPidGone_Windows pins the OpenProcess probe: a live pid and a
// pid that does not fit a DWORD are never "gone", a pid that no longer
// exists is, and so is one whose process exited while a handle to it is
// still held (the process object outlives the process).
func TestLocalPidGone_Windows(t *testing.T) {
	if localPidGone(os.Getpid()) {
		t.Error("localPidGone(own pid) = true, want false")
	}
	if localPidGone(math.MaxUint32 + 5) {
		t.Error("localPidGone(pid beyond uint32) = true, want false (must not wrap onto another process)")
	}
	if !localPidGone(999999) {
		t.Error("localPidGone(nonexistent pid) = false, want true")
	}

	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
	if !localPidGone(pid) {
		t.Errorf("localPidGone(exited pid %d) = false, want true", pid)
	}
}
