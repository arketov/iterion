package botregistry

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathKey_RelativeAndAbsoluteSpellingsAgree(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.bot")
	if err := os.WriteFile(file, []byte("workflow a:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	messy := filepath.Join(dir, "sub", "..", "a.bot")
	if PathKey(file) != PathKey(messy) {
		t.Fatalf("%q != %q", PathKey(file), PathKey(messy))
	}
}

func TestPathKey_AMissingPathKeysAsItsCleanedForm(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "nope", "x.bot")
	b := filepath.Join(dir, "nope", ".", "x.bot")
	if PathKey(a) != PathKey(b) {
		t.Fatalf("%q != %q", PathKey(a), PathKey(b))
	}
	if PathKey(a) == PathKey(filepath.Join(dir, "nope", "y.bot")) {
		t.Fatal("different missing paths must not share a key")
	}
}

func TestPathKey_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if PathKey(target) != PathKey(link) {
		t.Fatalf("%q != %q", PathKey(target), PathKey(link))
	}
}

// Windows ignores case: another drive-letter or file-name case is one path.
func TestPathKey_WindowsIgnoresCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive file systems only")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "Dev-Flow.bot")
	if err := os.WriteFile(file, []byte("workflow d:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if PathKey(file) != PathKey(strings.ToUpper(file)) {
		t.Fatalf("%q != %q", PathKey(file), PathKey(strings.ToUpper(file)))
	}
}
