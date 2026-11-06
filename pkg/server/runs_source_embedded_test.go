package server

import (
	"os"
	"path/filepath"
	"testing"
)

// The binary no longer carries recipes of its own, so a workflow path that is
// neither on disk nor in a cache an earlier release left is not resolved.
func TestResolveWorkflowPathHasNoBuiltInRecipes(t *testing.T) {
	srv := &Server{cfg: Config{StoreDir: t.TempDir()}}
	if got, err := srv.resolveWorkflowPath("feature-dev/main.bot", ""); err == nil {
		t.Fatalf("a built-in recipe name was resolved to %q: the binary carries none", got)
	}
}

// A run launched by name under an earlier release has its recipe cached under
// <store>/embedded-recipes/. It keeps resuming from those bytes; nothing new is
// ever written there.
func TestResolveWorkflowPathServesARecipeAnEarlierReleaseCached(t *testing.T) {
	store := t.TempDir()
	srv := &Server{cfg: Config{StoreDir: store}}
	cached := filepath.Join(store, "embedded-recipes", "feature-dev", "main.bot")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("workflow x:\n  entry: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := srv.resolveWorkflowPath("feature-dev/main.bot", "")
	if err != nil {
		t.Fatalf("a cached recipe must still resolve: %v", err)
	}
	if got != cached {
		t.Fatalf("resolved to %q, want the cached copy %q", got, cached)
	}

	// A path that climbs out of the cache, or names a directory, is not served.
	for _, name := range []string{"../escape.bot", "feature-dev", "nope/main.bot"} {
		if got, ok := srv.resolveCachedEmbeddedRecipe(name); ok {
			t.Errorf("resolveCachedEmbeddedRecipe(%q) = %q, want a miss", name, got)
		}
	}
}
