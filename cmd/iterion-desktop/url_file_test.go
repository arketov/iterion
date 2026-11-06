//go:build desktop

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDesktopURLFile_KeepsAnotherServersRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	path := filepath.Join(home, desktopURLFileName)
	// A later server wrote its record; the one that stops must not drop it.
	other := []byte(fmt.Sprintf(`{"url":"http://127.0.0.1:2/","pid":%d}`, os.Getpid()+1))
	if err := os.WriteFile(path, other, 0o600); err != nil {
		t.Fatal(err)
	}
	removeDesktopURLFile()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("another server's record was removed: %v", err)
	}
	if !bytes.Equal(got, other) {
		t.Fatalf("another server's record was modified: %s", got)
	}
	// Our own record: removed.
	writeDesktopURLFile("http://127.0.0.1:1/")
	removeDesktopURLFile()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("own record should be removed, stat err = %v", err)
	}
}
