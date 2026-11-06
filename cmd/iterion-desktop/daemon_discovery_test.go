//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// writeDaemonFileAt writes a registry file under an explicit name, so a test
// can plant a record whose project_dir encodes to a different key.
func writeDaemonFileAt(t *testing.T, name string, info daemonInfo) (path string, raw []byte) {
	t.Helper()
	dir := daemonRegistryDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

// The record found by the store key is judged as read: its project_dir may
// encode to another key (stale file), and that other file must not decide.
func TestFindDaemonForStorePath_JudgesTheFileReadByKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	key := encodeProjectDirKey(filepath.Join(home, "proj-a"))
	storePath := filepath.Join(home, "projects", key)
	otherDir := filepath.Join(home, "proj-b")

	t.Run("live daemon is found", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		url := "http://" + ln.Addr().String() + "/"
		writeDaemonFileAt(t, key, daemonInfo{URL: url, PID: os.Getpid(), ProjectDir: otherDir})
		got, ok := findDaemonForStorePath(storePath)
		if !ok || got != url {
			t.Fatalf("findDaemonForStorePath = %q, %v; want %q, true", got, ok, url)
		}
	})

	t.Run("dead daemon prunes the file read, not the re-encoded one", func(t *testing.T) {
		keyPath, _ := writeDaemonFileAt(t, key, daemonInfo{URL: "http://127.0.0.1:1/", PID: 0, ProjectDir: otherDir})
		otherPath, _ := writeDaemonFileAt(t, encodeProjectDirKey(otherDir), daemonInfo{URL: "http://127.0.0.1:1/", PID: 0, ProjectDir: otherDir})
		if got, ok := findDaemonForStorePath(storePath); ok {
			t.Fatalf("findDaemonForStorePath = %q, true; want a miss for a dead daemon", got)
		}
		if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
			t.Errorf("stale file read by key should be removed, stat err = %v", err)
		}
		if _, err := os.Stat(otherPath); err != nil {
			t.Errorf("file of the re-encoded key must stay untouched: %v", err)
		}
	})
}

func TestRemoveDaemonInfo_KeepsAnotherDaemonsRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	project := filepath.Join(home, "project")
	if err := writeDaemonInfo(project, "http://127.0.0.1:1/"); err != nil {
		t.Fatal(err)
	}
	path := daemonInfoPath(project)
	// Another daemon wrote its record over ours before we shut down.
	other, err := json.Marshal(daemonInfo{URL: "http://127.0.0.1:2/", PID: os.Getpid() + 1, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, other, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeDaemonInfo(project); err != nil {
		t.Fatalf("removeDaemonInfo: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("another daemon's record was removed: %v", err)
	}
	if !bytes.Equal(got, other) {
		t.Fatalf("another daemon's record was modified: %s", got)
	}
	// Our own record: removed.
	if err := writeDaemonInfo(project, "http://127.0.0.1:1/"); err != nil {
		t.Fatal(err)
	}
	if err := removeDaemonInfo(project); err != nil {
		t.Fatalf("removeDaemonInfo: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("own record should be removed, stat err = %v", err)
	}
	// Missing file: no error.
	if err := removeDaemonInfo(project); err != nil {
		t.Fatalf("removeDaemonInfo on a missing file: %v", err)
	}
}

func TestRemoveDaemonInfo_WaitsOutAReaderHoldingTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	project := filepath.Join(home, "project")
	if err := writeDaemonInfo(project, "http://127.0.0.1:1/"); err != nil {
		t.Fatal(err)
	}
	// On Windows a Go handle has no FILE_SHARE_DELETE, so the delete fails
	// with a sharing violation while the reader holds the file; elsewhere
	// the delete never blocks and this passes trivially.
	f, err := os.Open(daemonInfoPath(project))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	timer := time.AfterFunc(300*time.Millisecond, func() { _ = f.Close() })
	t.Cleanup(func() { timer.Stop() })

	if err := removeDaemonInfo(project); err != nil {
		t.Fatalf("removeDaemonInfo: %v", err)
	}
	if _, err := os.Stat(daemonInfoPath(project)); !os.IsNotExist(err) {
		t.Fatalf("record should be removed once the reader closes, stat err = %v", err)
	}
}

func TestProjectDirFromKey_HashedKey(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path joining")
	}
	const project = `C:\my-app`
	key := encodeProjectDirKey(project)
	if !strings.Contains(key, "~") {
		t.Fatalf("a Windows path with a dash must get a hash suffix, got %q", key)
	}
	only := func(dir string) func(string) bool {
		return func(p string) bool { return p == `C:\` || p == dir }
	}
	if got := decodeHashedWindowsProjectKey(key, only(project)); got != project {
		t.Errorf("decode(%q) = %q, want %q", key, got, project)
	}
	// Only the sibling `C:\my\app` exists: same stem, different hash.
	if got := decodeHashedWindowsProjectKey(key, func(p string) bool {
		return p == `C:\` || p == `C:\my` || p == `C:\my\app`
	}); got != "" {
		t.Errorf("a sibling directory with the same stem decoded to %q, want empty", got)
	}
	// A legacy key keeps the plain decode.
	if got := decodeHashedWindowsProjectKey("-C--a-b", only(`C:\a-b`)); got != `C:\a-b` {
		t.Errorf("legacy key decoded to %q", got)
	}
}
