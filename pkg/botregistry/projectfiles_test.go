package botregistry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProjectFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverProjectFiles(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "test.bot"), "workflow t {}")
	writeProjectFile(t, filepath.Join(root, "flows", "dev-flow.bot"), "workflow d {}")
	// A bundle's main.bot is not a loose file.
	writeProjectFile(t, filepath.Join(root, "mybundle", "main.bot"), "workflow b {}")
	writeProjectFile(t, filepath.Join(root, "mybundle", "manifest.yaml"), "name: mybundle\n")
	// Skipped trees.
	writeProjectFile(t, filepath.Join(root, "node_modules", "x", "n.bot"), "")
	writeProjectFile(t, filepath.Join(root, ".git", "g.bot"), "")
	// A catalog root passed as excluded.
	writeProjectFile(t, filepath.Join(root, "bots", "solo.bot"), "")
	// Not a .bot file.
	writeProjectFile(t, filepath.Join(root, "notes.txt"), "")

	got := DiscoverProjectFiles(root, []string{filepath.Join(root, "bots")})
	var rels []string
	for _, e := range got {
		rels = append(rels, e.RelPath)
		if !e.Enabled || !e.ManifestEnabled {
			t.Errorf("%s: loose files report enabled", e.RelPath)
		}
		if !filepath.IsAbs(e.Path) {
			t.Errorf("%s: Path must be absolute, got %s", e.RelPath, e.Path)
		}
	}
	want := []string{"flows/dev-flow.bot", "test.bot"}
	if len(rels) != len(want) || rels[0] != want[0] || rels[1] != want[1] {
		t.Fatalf("rels = %v, want %v", rels, want)
	}
	if got[1].Name != "test" || got[0].Name != "flows/dev-flow" {
		t.Fatalf("names = %q, %q", got[0].Name, got[1].Name)
	}
}

func TestDiscoverProjectFilesEmptyWorkDir(t *testing.T) {
	if got := DiscoverProjectFiles("", nil); got != nil {
		t.Fatalf("empty workDir must yield nil, got %v", got)
	}
}
