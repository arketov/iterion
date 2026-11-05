package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// A plugin directory without plugin.yaml is skipped at load (LoadSkips) and is
// not in the registry, but it is what the "plugin(s) were skipped" warning
// tells the operator to uninstall — so Remove must delete it rather than
// answer "not found".
func TestRemoveDeletesAPluginWhoseManifestNeverLoaded(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, pluginsSubdir, "broken-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("no manifest here"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reg.LoadSkips()) != 1 {
		t.Fatalf("setup: want one skip, got %v", reg.LoadSkips())
	}
	if err := reg.Remove("broken-demo"); err != nil {
		t.Fatalf("Remove of a skipped plugin: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin directory still exists: %v", err)
	}
	reg2, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reg2.LoadSkips()) != 0 {
		t.Fatalf("the skip warning must be gone after uninstall: %v", reg2.LoadSkips())
	}
	if err := reg2.Remove("never-installed"); err == nil {
		t.Fatal("removing an unknown plugin must still fail")
	}
}

// A builtin plugin keeps its {{plugin.cache}} under <home>/plugins/<name>/, so a
// healthy host can hold a directory named like a builtin with no manifest in it.
// That is the builtin's scratch space, not a broken install: no load skip.
func TestManifestlessDirOfABuiltinIsNotASkip(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, pluginsSubdir, "repo-falcon", "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if skips := reg.LoadSkips(); len(skips) != 0 {
		t.Fatalf("a builtin's cache directory must not be reported as a skipped plugin: %v", skips)
	}
	if _, ok := reg.Get("repo-falcon"); !ok {
		t.Fatal("the builtin must still load")
	}
	// A manifestless directory with no builtin behind its name stays a skip.
	if err := os.MkdirAll(filepath.Join(home, pluginsSubdir, "not-a-builtin"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg2, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reg2.LoadSkips()) != 1 {
		t.Fatalf("want exactly the unknown directory skipped, got %v", reg2.LoadSkips())
	}
}
