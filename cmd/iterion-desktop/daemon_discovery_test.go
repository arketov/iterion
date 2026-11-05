//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeWindowsProjectKey(t *testing.T) {
	existing := map[string]bool{`C:\Users\me\proj`: true, `D:\`: true}
	exists := func(p string) bool { return existing[p] }
	cases := []struct{ key, want string }{
		{"-C--Users-me-proj", `C:\Users\me\proj`},
		{"-C--Users-me-missing", ""}, // decoded dir does not exist
		{"-C--Users-me-my-proj", ""}, // dash in a folder name: not found, no guess
		{"-home-user-proj", ""},      // unix-shaped key
		{"-C-Users", ""},             // no drive separator
		{"--server-share", ""},       // UNC shape
		{"-1--x", ""},                // not a drive letter
		{"-C--Users--me-proj", ""},   // empty segment
		{"-C--Users-me-proj-", ""},   // trailing separator
		{"-C--", ""},                 // bare drive root
		{"", ""},
	}
	for _, c := range cases {
		if got := decodeWindowsProjectKey(c.key, exists); got != c.want {
			t.Errorf("decodeWindowsProjectKey(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestDecodeUnixProjectKey(t *testing.T) {
	if got := decodeUnixProjectKey("-home-user-proj"); got != "/home/user/proj" {
		t.Errorf("got %q", got)
	}
	if got := decodeUnixProjectKey("home"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestRemoveDaemonFileIfUnchanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.json")
	if err := os.WriteFile(p, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Content replaced by a new daemon after the stale read: must survive.
	removeDaemonFileIfUnchanged(p, []byte(`{"pid":2}`))
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file was removed although content differs: %v", err)
	}
	// Unchanged content: removed.
	removeDaemonFileIfUnchanged(p, []byte(`{"pid":1}`))
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, stat err = %v", err)
	}
	// Missing file: no panic.
	removeDaemonFileIfUnchanged(p, []byte(`{"pid":1}`))
}
