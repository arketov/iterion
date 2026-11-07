package memory

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceMemoryDir_HonorsITERIONHOME(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ITERION_HOME", tmp)
	got := WorkspaceMemoryDir("/workspaces/iterion")
	want := filepath.Join(tmp, "projects", "-workspaces-iterion", "memory")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWorkspaceMemoryDir_EmptyWorkDir(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	if got := WorkspaceMemoryDir(""); got != "" {
		t.Fatalf("empty workDir: got %q", got)
	}
}

func TestValidateScopeName(t *testing.T) {
	good := []string{"session-continuity", "whats-next", "secured-renovacy", "learnings"}
	for _, s := range good {
		if err := ValidateScopeName(s); err != nil {
			t.Fatalf("%q rejected: %v", s, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", "a\\b"}
	for _, s := range bad {
		if err := ValidateScopeName(s); err == nil {
			t.Fatalf("%q accepted (expected rejection)", s)
		}
	}
}

func TestScope_Resolve_RejectsEscape(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, err := OpenScope("/tmp/wn", "session-continuity")
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}
	cases := []string{"../escape.md", "sub/../../escape.md", "/etc/passwd"}
	for _, rel := range cases {
		if _, err := scope.Resolve(rel); err == nil {
			t.Fatalf("%q: expected escape rejection", rel)
		}
	}
}

func TestScope_WriteReadList_Roundtrip(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, err := OpenScope("/tmp/wn", "session-continuity")
	if err != nil {
		t.Fatalf("OpenScope: %v", err)
	}

	if err := scope.Write("CONTEXT_BRIEF.md", []byte("# brief")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := scope.Write("decisions/2026-05-18.md", []byte("dropped feature X")); err != nil {
		t.Fatalf("Write nested: %v", err)
	}

	got, err := scope.Read("CONTEXT_BRIEF.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "# brief" {
		t.Fatalf("content: %q", string(got))
	}

	top, err := scope.List("")
	if err != nil {
		t.Fatalf("List root: %v", err)
	}
	if len(top) != 1 || filepath.Base(top[0]) != "CONTEXT_BRIEF.md" {
		t.Fatalf("List root: %v", top)
	}

	nested, err := scope.List("decisions")
	if err != nil {
		t.Fatalf("List nested: %v", err)
	}
	if len(nested) != 1 || filepath.Base(nested[0]) != "2026-05-18.md" {
		t.Fatalf("List nested: %v", nested)
	}
}

func TestScope_Read_MissingFile(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	_, err := scope.Read("absent.md")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: want os.ErrNotExist, got %v", err)
	}
}

func TestScope_List_MissingDirIsEmpty(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	got, err := scope.List("never-created")
	if err != nil {
		t.Fatalf("List missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestScope_Autoload_EmptyPatternsReturnsNothing(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	_ = scope.Write("INDEX.md", []byte("# index"))
	_ = scope.Write("other.md", []byte("# other"))

	got, err := scope.Autoload(nil)
	if err != nil {
		t.Fatalf("Autoload: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty patterns must return nothing (auto-index covers the index), got %+v", got)
	}
}

func TestScope_Autoload_GlobUnionDeduped(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	_ = scope.Write("CONTEXT_BRIEF.md", []byte("# brief"))
	_ = scope.Write("INDEX.md", []byte("# index"))
	_ = scope.Write("decisions/d1.md", []byte("d1"))

	got, err := scope.Autoload([]string{"*.md", "*.md", "CONTEXT_BRIEF.md"})
	if err != nil {
		t.Fatalf("Autoload: %v", err)
	}
	// *.md alone should produce 2 entries; dedup prevents the
	// second + CONTEXT_BRIEF.md from re-adding.
	if len(got) != 2 {
		t.Fatalf("dedup: got %d entries (%+v)", len(got), got)
	}
	for _, e := range got {
		if strings.Contains(e.Path, "decisions") {
			t.Fatalf("subfolder leaked into top glob: %+v", e)
		}
	}
}

func TestScope_Autoload_MissingPatternIsNoOp(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	got, err := scope.Autoload([]string{"absent.md"})
	if err != nil {
		t.Fatalf("Autoload: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing files should be skipped, got %+v", got)
	}
}

func TestScope_Autoload_RejectsEscape(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	scope, _ := OpenScope("/tmp/wn", "session-continuity")
	if _, err := scope.Autoload([]string{"../escape*.md"}); err == nil {
		t.Fatalf("expected escape rejection")
	}
}

func TestOpenScope_BadScopeName(t *testing.T) {
	t.Setenv("ITERION_HOME", t.TempDir())
	if _, err := OpenScope("/tmp/wn", "../session"); err == nil {
		t.Fatalf("expected rejection")
	}
}

func TestProjectKey_FollowsLegacySlotDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs a Windows-style absolute path")
	}
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	const work = `C:\my-app`
	fresh := ProjectKey(work)
	if !strings.Contains(fresh, "~") {
		t.Fatalf("fresh home: key %q should carry the hash suffix", fresh)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "-C--my-app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(work); got != "-C--my-app" {
		t.Errorf("legacy slot present: ProjectKey = %q, want %q", got, "-C--my-app")
	}
	want := filepath.Join(home, "projects", "-C--my-app", "memory")
	if got := WorkspaceMemoryDir(work); got != want {
		t.Errorf("WorkspaceMemoryDir = %q, want %q", got, want)
	}
}
