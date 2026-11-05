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
	body := "workflow w:\n"
	if filepath.Base(p) == "manifest.yaml" {
		body = "" // an empty manifest is still a manifest of ours
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
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

func TestListWorkflowFiles_Scope(t *testing.T) {
	root := t.TempDir()
	// Root: single files only, no recursion.
	writeListFixture(t, root, "dev-flow.bot")
	writeListFixture(t, root, "elsewhere/deep.bot")
	// bots/: a bundle is one row (main.bot); its lib/ parts are not rows.
	writeListFixture(t, root, "bots/x/manifest.yaml")
	writeListFixture(t, root, "bots/x/main.bot")
	writeListFixture(t, root, "bots/x/lib/nodes.bot")
	writeListFixture(t, root, "bots/review.bot")
	// examples/ and .botz/ are discovery roots too.
	writeListFixture(t, root, "examples/demo.bot")
	writeListFixture(t, root, ".botz/packed/manifest.yaml")
	writeListFixture(t, root, ".botz/packed/main.bot")
	// A nested clone and a linked worktree inside a root: each holds .git.
	writeListFixture(t, root, "bots/clone/a.bot")
	if err := os.MkdirAll(filepath.Join(root, "bots", "clone", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeListFixture(t, root, "examples/wt/b.bot")
	writeListFixture(t, root, "examples/wt/.git") // .git as a file, like a worktree
	// A nested clone of the whole project below the root is never reached.
	writeListFixture(t, root, "build-desktop/src/bots/y/main.bot")

	files, truncated := listWorkflowFiles(root, 0)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Name] = f.Kind
	}
	want := map[string]string{
		"bots/x/main.bot":       fileKindBundle,
		"bots/review.bot":       fileKindFile,
		"examples/demo.bot":     fileKindFile,
		".botz/packed/main.bot": fileKindBundle,
		"dev-flow.bot":          fileKindFile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListWorkflowFiles_ProjectRootWithGitIsStillListed(t *testing.T) {
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
