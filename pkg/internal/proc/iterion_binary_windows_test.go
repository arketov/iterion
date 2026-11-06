//go:build windows

package proc

import (
	"os"
	"path/filepath"
	"testing"
)

// ITERION_BIN pointing at a real iterion.exe is accepted on Windows, where
// Go reports no execute bits and the Unix 0o111 check rejected every file.
func TestLocateIterionBinary_WindowsExeEnvOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "iterion.exe")
	if err := os.WriteFile(p, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ITERION_BIN", p)
	restore := SetInstallPathCandidates(func() []string { return nil })
	defer restore()

	if got := LocateIterionBinary(); got != p {
		t.Fatalf("LocateIterionBinary() = %q, want %q", got, p)
	}
}
