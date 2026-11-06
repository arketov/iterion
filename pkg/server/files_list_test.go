package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
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

	files, _, truncated, _ := listWorkflowFiles(root, botregistry.DefaultPaths(root), 0)
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
	files, _, _, _ := listWorkflowFiles(root, botregistry.DefaultPaths(root), 0)
	if got := listedNames(files); !reflect.DeepEqual(got, []string{"a.bot"}) {
		t.Fatalf("got %v", got)
	}
}

// The cap keeps the bundles: a project with many loose files still lists its
// bundles, and the response says how many rows the rule found.
func TestListWorkflowFiles_Limit(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.bot", "b.bot", "c.bot"} {
		writeListFixture(t, root, n)
	}
	writeListFixture(t, root, ".botz/packed/manifest.yaml")
	writeListFixture(t, root, ".botz/packed/main.bot")
	roots := botregistry.DefaultPaths(root)
	files, total, truncated, _ := listWorkflowFiles(root, roots, 2)
	if !truncated || len(files) != 2 || total != 4 {
		t.Fatalf("got %d files, total=%d, truncated=%v; want 2, 4, true", len(files), total, truncated)
	}
	if got := listedNames(files); !reflect.DeepEqual(got, []string{".botz/packed/main.bot", "a.bot"}) {
		t.Fatalf("the bundle must survive the cap: %v", got)
	}
	files, _, truncated, _ = listWorkflowFiles(root, roots, 4)
	if truncated || len(files) != 4 {
		t.Fatalf("at exactly the limit: got %d, truncated=%v", len(files), truncated)
	}
}

// A bundle directory with a manifest and no main.bot is reported instead of
// vanishing from the list.
func TestListWorkflowFiles_ReportsAnUnopenableBundle(t *testing.T) {
	root := t.TempDir()
	writeListFixture(t, root, "bots/broken/manifest.yaml")
	files, _, _, skipped := listWorkflowFiles(root, botregistry.DefaultPaths(root), 0)
	if len(files) != 0 {
		t.Fatalf("got %v", listedNames(files))
	}
	if len(skipped) != 1 || skipped[0].Path != "bots/broken" {
		t.Fatalf("skipped = %+v", skipped)
	}
}

// --bots-path pins the catalog, so it pins the list the editor shows too.
func TestHandleListFiles_FollowsBotsPath(t *testing.T) {
	root := t.TempDir()
	writeListFixture(t, root, "flows/dev-flow.bot")
	writeListFixture(t, root, "bots/ignored.bot")
	s := &Server{cfg: Config{WorkDir: root, Bots: BotsConfig{Paths: []string{filepath.Join(root, "flows")}}}}
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var resp listFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := listedNames(resp.Files); !reflect.DeepEqual(got, []string{"flows/dev-flow.bot"}) {
		t.Fatalf("got %v", got)
	}
}

func TestHandleListFiles_TruncatedCarriesTheTotal(t *testing.T) {
	t.Setenv("ITERION_STUDIO_FILES_LIMIT", "1")
	root := t.TempDir()
	writeListFixture(t, root, "a.bot")
	writeListFixture(t, root, "b.bot")
	s := &Server{cfg: Config{WorkDir: root}}
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var resp listFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Truncated || resp.Total != 2 || len(resp.Files) != 1 {
		t.Fatalf("got %+v", resp)
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
