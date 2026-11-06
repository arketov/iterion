package plugin

import (
	"os"
	"path/filepath"
	"strings"
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

// The install directory is the plugin's identity (the enable key too): a
// directory whose plugin.yaml names another plugin is broken like an unreadable
// manifest — a load skip named after the DIRECTORY, so `plugin uninstall <dir>`
// is the remedy — not a plugin silently loaded under the manifest's name.
func TestInstalledDirWithMismatchedManifestNameIsASkip(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, pluginsSubdir, "foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: bar\ncontributes:\n  lifecycle:\n    index: echo hi\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := reg.Get("bar"); ok {
		t.Fatal("a directory whose manifest names another plugin must not load")
	}
	skips := reg.LoadSkips()
	if len(skips) != 1 || !strings.HasPrefix(skips[0], "foo: ") || !strings.Contains(skips[0], "bar") {
		t.Fatalf("want one skip prefixed with the directory name and naming the manifest name, got %v", skips)
	}
	if err := reg.Remove("foo"); err != nil {
		t.Fatalf("Remove of the mismatched directory: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin directory still exists: %v", err)
	}
	reg2, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reg2.LoadSkips()) != 0 {
		t.Fatalf("the skip must be gone after uninstall: %v", reg2.LoadSkips())
	}
}

// Remove takes the name as a directory name: a broken bar/ next to a healthy
// foo/ that declares `name: bar` must lose bar/, never foo/.
func TestRemoveTargetsTheDirectoryNamedNotAnotherPluginsManifestName(t *testing.T) {
	home := t.TempDir()
	barDir := filepath.Join(home, pluginsSubdir, "bar")
	fooDir := filepath.Join(home, pluginsSubdir, "foo")
	for _, d := range []string{barDir, fooDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(barDir, ManifestFile), []byte(": : bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fooDir, ManifestFile), []byte("name: bar\ncontributes:\n  lifecycle:\n    index: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.Remove("bar"); err != nil {
		t.Fatalf("Remove(bar): %v", err)
	}
	if _, err := os.Stat(barDir); !os.IsNotExist(err) {
		t.Fatalf("bar/ must be removed: %v", err)
	}
	if _, err := os.Stat(fooDir); err != nil {
		t.Fatalf("foo/ belongs to another directory name and must stay: %v", err)
	}
	if err := reg.Remove("foo"); err != nil {
		t.Fatalf("Remove(foo): %v", err)
	}
	if _, err := os.Stat(fooDir); !os.IsNotExist(err) {
		t.Fatalf("foo/ must be removed: %v", err)
	}
}

// A broken directory named like a builtin is removable (the skip advises it)
// without touching the builtin's enable state; a stale plugins.yaml entry left by
// a hand-deleted directory is cleared by an explicit uninstall.
func TestRemoveClearsABrokenDirNamedLikeABuiltinAndStaleState(t *testing.T) {
	home := t.TempDir()
	reg, err := LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.SetEnabled("repo-falcon", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	dir := filepath.Join(home, pluginsSubdir, "repo-falcon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(": : bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err = LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reg.LoadSkips()) != 1 {
		t.Fatalf("setup: want one skip, got %v", reg.LoadSkips())
	}
	if err := reg.Remove("repo-falcon"); err != nil {
		t.Fatalf("Remove of a broken dir named like a builtin: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("broken directory still exists: %v", err)
	}
	reg, err = LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p, ok := reg.Get("repo-falcon")
	if !ok || !p.Builtin {
		t.Fatal("the builtin must still be there")
	}
	if !reg.IsEnabled("repo-falcon") {
		t.Fatal("the builtin's enable state must survive removing the broken directory")
	}

	// Stale entry: enabled in plugins.yaml, no directory.
	if err := os.WriteFile(filepath.Join(home, stateFile), []byte("enabled:\n  ghost: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err = LoadFromForTest(home, home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := reg.Remove("ghost"); err != nil {
		t.Fatalf("Remove of a stale entry: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ghost") {
		t.Fatalf("stale entry still in %s: %s", stateFile, data)
	}
	if err := reg.Remove("ghost"); err == nil {
		t.Fatal("a second Remove of the gone entry must report not found")
	}
}
