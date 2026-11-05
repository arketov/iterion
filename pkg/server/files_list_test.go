package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeListFixture(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("workflow w:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listedNames(es []fileEntry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestIsBundleFragment(t *testing.T) {
	cases := map[string]bool{
		"dev-flow.bot":             false,
		"bots/review.bot":          false,
		"bots/x/main.bot":          false,
		"bots/x/lib/nodes.bot":     true,
		"bots/x/lib/prompts.bot":   true,
		"bots/x/other.bot":         true,
		"sub/bots/x/main.bot":      false,
		"sub/bots/x/lib/nodes.bot": true,
		"examples/a.bot":           false,
	}
	for rel, want := range cases {
		if got := isBundleFragment(rel); got != want {
			t.Errorf("isBundleFragment(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestListWorkflowFiles_SkipsNestedReposAndFragments(t *testing.T) {
	root := t.TempDir()
	writeListFixture(t, root, "dev-flow.bot")
	writeListFixture(t, root, "bots/review.bot")
	writeListFixture(t, root, "bots/x/main.bot")
	writeListFixture(t, root, "bots/x/lib/nodes.bot")
	// A nested clone and a linked worktree: each has a .git entry.
	writeListFixture(t, root, "build/src/bots/y/main.bot")
	writeListFixture(t, root, "build/src/.git") // .git as a file, like a worktree
	writeListFixture(t, root, "clone/a.bot")
	if err := os.MkdirAll(filepath.Join(root, "clone", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hidden directory is still skipped.
	writeListFixture(t, root, ".worktree/w/a.bot")

	files, truncated := listWorkflowFiles(root, 0)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	got := listedNames(files)
	want := []string{"bots/review.bot", "bots/x/main.bot", "dev-flow.bot"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListWorkflowFiles_RootWithGitIsStillWalked(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeListFixture(t, root, "a.bot")
	files, _ := listWorkflowFiles(root, 0)
	if got := listedNames(files); !reflect.DeepEqual(got, []string{"a.bot"}) {
		t.Fatalf("got %v", got)
	}
}

func TestListWorkflowFiles_Limit(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.bot", "b.bot", "c.bot"} {
		writeListFixture(t, root, n)
	}
	files, truncated := listWorkflowFiles(root, 2)
	if !truncated || len(files) != 2 {
		t.Fatalf("got %d files, truncated=%v; want 2, true", len(files), truncated)
	}
	files, truncated = listWorkflowFiles(root, 3)
	if truncated || len(files) != 3 {
		t.Fatalf("at exactly the limit: got %d, truncated=%v", len(files), truncated)
	}
}

func TestListFilesLimit_EnvOverride(t *testing.T) {
	t.Setenv("ITERION_STUDIO_FILES_LIMIT", "")
	if got := listFilesLimit(); got != defaultListFilesLimit {
		t.Fatalf("default: got %d", got)
	}
	t.Setenv("ITERION_STUDIO_FILES_LIMIT", "7")
	if got := listFilesLimit(); got != 7 {
		t.Fatalf("override: got %d", got)
	}
	t.Setenv("ITERION_STUDIO_FILES_LIMIT", "0")
	if got := listFilesLimit(); got != 0 {
		t.Fatalf("zero lifts the cap: got %d", got)
	}
	t.Setenv("ITERION_STUDIO_FILES_LIMIT", "junk")
	if got := listFilesLimit(); got != defaultListFilesLimit {
		t.Fatalf("malformed: got %d", got)
	}
}
