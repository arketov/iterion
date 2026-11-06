//go:build windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func assertNoAtomicTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".atomic-") {
			t.Errorf("temp file %q left behind", e.Name())
		}
	}
}

// os.Open does not share DELETE, so a rename onto the open file fails at once
// with ACCESS_DENIED / SHARING_VIOLATION. The write must wait for the handle.
func TestWriteFileAtomic_RetriesWhileDestinationIsHeldOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.json")
	if err := writeFileAtomic(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(200 * time.Millisecond)
		held.Close()
	}()
	t.Cleanup(func() { <-released })

	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic while destination held open: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	assertNoAtomicTemps(t, dir)
}

func TestWriteFileAtomic_GivesUpAfterRetryWindow(t *testing.T) {
	old := renameRetryWindow
	renameRetryWindow = 100 * time.Millisecond
	t.Cleanup(func() { renameRetryWindow = old })

	dir := t.TempDir()
	path := filepath.Join(dir, "card.json")
	if err := writeFileAtomic(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	werr := writeFileAtomic(path, []byte("new"), 0o600)
	held.Close()

	if werr == nil {
		t.Fatal("writeFileAtomic succeeded while the destination stayed open")
	}
	if !isTransientRenameErr(werr) {
		t.Errorf("error = %v, want one wrapping ACCESS_DENIED or SHARING_VIOLATION", werr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "old" {
		t.Errorf("prior content = %q, want %q", got, "old")
	}
	assertNoAtomicTemps(t, dir)
}

func TestIsTransientRenameErr(t *testing.T) {
	link := func(e error) error { return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: e} }
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"access denied", windows.ERROR_ACCESS_DENIED, true},
		{"sharing violation", windows.ERROR_SHARING_VIOLATION, true},
		{"lock violation", windows.ERROR_LOCK_VIOLATION, true},
		{"link access denied", link(windows.ERROR_ACCESS_DENIED), true},
		{"link sharing violation", link(windows.ERROR_SHARING_VIOLATION), true},
		{"link lock violation", link(windows.ERROR_LOCK_VIOLATION), true},
		{"file not found", windows.ERROR_FILE_NOT_FOUND, false},
		{"path not found", windows.ERROR_PATH_NOT_FOUND, false},
		{"link file not found", link(windows.ERROR_FILE_NOT_FOUND), false},
		{"unrelated", errors.New("boom"), false},
	} {
		if got := isTransientRenameErr(tc.err); got != tc.want {
			t.Errorf("%s: isTransientRenameErr = %v, want %v", tc.name, got, tc.want)
		}
	}
}
