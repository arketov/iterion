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
	dir := filepath.Join(home, pluginsSubdir, "repo-falcon")
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
	if err := reg.Remove("repo-falcon"); err != nil {
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
