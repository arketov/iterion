//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeWindowsProjectKeyRejectsOtherShapes(t *testing.T) {
	anyDir := func(string) bool { return true }
	for _, key := range []string{
		"-home-user-proj", // unix-shaped key
		"-C-Users",        // no drive separator
		"--server-share",  // UNC shape
		"-1--x",           // not a drive letter
		"-C--",            // bare drive root
		"",
	} {
		if got := decodeWindowsProjectKey(key, anyDir); got != "" {
			t.Errorf("decodeWindowsProjectKey(%q) = %q, want empty", key, got)
		}
	}
}

func TestDecodeWindowsProjectKeyWalksFromDrive(t *testing.T) {
	// Only the drive-root join is Windows-specific; the walk itself is
	// covered by TestResolveKeyPath on a real temp tree.
	seen := ""
	got := decodeWindowsProjectKey("-C--a", func(p string) bool { seen = p; return false })
	if got != "" {
		t.Errorf("got %q, want empty when nothing exists", got)
	}
	if seen == "" {
		t.Error("isDir was never consulted")
	}
}

func TestResolveKeyPath(t *testing.T) {
	mk := func(root string, parts ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("dash in folder name", func(t *testing.T) {
		root := t.TempDir()
		mk(root, "work", "my-proj")
		want := filepath.Join(root, "work", "my-proj")
		if got := resolveKeyPath(root, "work-my-proj", dirExists); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("dash as separator", func(t *testing.T) {
		root := t.TempDir()
		mk(root, "a", "b")
		want := filepath.Join(root, "a", "b")
		if got := resolveKeyPath(root, "a-b", dirExists); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("ambiguous a-b and a/b", func(t *testing.T) {
		root := t.TempDir()
		mk(root, "a-b")
		mk(root, "a", "b")
		if got := resolveKeyPath(root, "a-b", dirExists); got != "" {
			t.Errorf("got %q, want empty for ambiguous key", got)
		}
	})
	t.Run("mixed dashes", func(t *testing.T) {
		root := t.TempDir()
		mk(root, "itterion-test", "iterion")
		want := filepath.Join(root, "itterion-test", "iterion")
		if got := resolveKeyPath(root, "itterion-test-iterion", dirExists); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("nothing exists", func(t *testing.T) {
		if got := resolveKeyPath(t.TempDir(), "x-y", dirExists); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("regular file is not a directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "f"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := resolveKeyPath(root, "f", dirExists); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("probe budget bounds long keys", func(t *testing.T) {
		probes := 0
		onlyAs := func(p string) bool {
			probes++
			return !strings.ContainsAny(filepath.Base(p), "xyz")
		}
		// The dead end in the last token makes the search exponential;
		// the budget must stop it and the result must not be trusted.
		key := strings.Repeat("a-", 40) + "xyz"
		if got := resolveKeyPath(t.TempDir(), key, onlyAs); got != "" {
			t.Errorf("got %q, want empty", got)
		}
		if probes > maxKeyProbes {
			t.Errorf("probes = %d, exceeds budget %d", probes, maxKeyProbes)
		}
	})
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
