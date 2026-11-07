package dispatcher

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFlow(t *testing.T, dir string) string {
	t.Helper()
	wf := filepath.Join(dir, "flow.bot")
	if err := os.WriteFile(wf, []byte("workflow x: x -> done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wf
}

// A config saved by an older version is handed to MigrateConfig at load; when
// it says it changed the config, the migrated config is what the manager
// holds AND what is on disk, so the settings form shows it too.
func TestNewManager_MigratesAndPersistsTheLoadedConfig(t *testing.T) {
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := newTestStoreDir(t)
	wf := writeFlow(t, dir)
	cfgPath := filepath.Join(dir, "dispatcher", "dispatcher.json")
	body := `{"workflow":"` + strings.ReplaceAll(wf, `\`, `\\`) + `","tracker":{"kind":"native"},"workspace":{"root":"relroot"},"hooks":{"after_create":{"script":"set -e\necho old"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
		MigrateConfig: func(c *Config) bool {
			if c.Hooks.AfterCreate == nil || c.Hooks.AfterCreate.Shell != "" {
				return false
			}
			c.Hooks.AfterCreate.Script = "Write-Output new"
			c.Hooks.AfterCreate.Shell = "pwsh"
			return true
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if h := m.Config().Hooks.AfterCreate; h.Shell != "pwsh" || h.Script != "Write-Output new" {
		t.Fatalf("held config not migrated: %+v", h)
	}
	disk, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(disk), `"shell": "pwsh"`) {
		t.Fatalf("migrated config not persisted: %v\n%s", err, disk)
	}
	// Only the hook was rewritten: the loaded config has the relative
	// workspace root made absolute, and writing that back would also write
	// expanded `$TOKEN`s in clear.
	if !strings.Contains(string(disk), `"root": "relroot"`) {
		t.Fatalf("the rest of the file must stay as written: %s", disk)
	}
}

// AdaptRuntime rewrites only the copy the dispatcher runs: the saved file —
// possibly shared with another OS — is byte-for-byte what it was, at load and
// after a save of the saved form.
func TestNewManager_AdaptRuntimeTouchesOnlyTheRunningCopy(t *testing.T) {
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := newTestStoreDir(t)
	wf := writeFlow(t, dir)
	cfgPath := filepath.Join(dir, "dispatcher", "dispatcher.json")
	body := `{"workflow":"` + strings.ReplaceAll(wf, `\`, `\\`) + `","tracker":{"kind":"native"},"hooks":{"after_create":{"script":"Write-Output old","shell":"pwsh"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
		AdaptRuntime: func(c *Config) bool {
			c.Hooks.AfterCreate.Script = "echo adapted"
			c.Hooks.AfterCreate.Shell = ""
			return true
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if h := m.Config().Hooks.AfterCreate; h.Script != "echo adapted" || h.Shell != "" {
		t.Fatalf("running copy not adapted: %+v", h)
	}
	if h := m.RawConfig().Hooks.AfterCreate; h.Script != "Write-Output old" || h.Shell != "pwsh" {
		t.Fatalf("saved form must stay as written: %+v", h)
	}
	disk, err := os.ReadFile(cfgPath)
	if err != nil || string(disk) != body {
		t.Fatalf("the saved file must be untouched at load: %v\n%s", err, disk)
	}
	if err := m.SaveConfig(m.RawConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	disk, _ = os.ReadFile(cfgPath)
	if strings.Contains(string(disk), "adapted") || !strings.Contains(string(disk), "Write-Output old") {
		t.Fatalf("a save must keep the saved form on disk: %s", disk)
	}
	if h := m.Config().Hooks.AfterCreate; h.Script != "echo adapted" {
		t.Fatalf("running copy after save: %+v", h)
	}
}

// POST /defaults/apply never overwrites a saved config unless the operator
// asked for it with ?replace=1 ("back to the default settings").
func TestApplyDefaults_ExistingConfigNeedsReplace(t *testing.T) {
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := newTestStoreDir(t)
	wf := writeFlow(t, dir)
	cfgPath := filepath.Join(dir, "dispatcher", "dispatcher.json")
	if err := os.WriteFile(cfgPath, []byte(`{"workflow":"`+strings.ReplaceAll(wf, `\`, `\\`)+`","tracker":{"kind":"native"},"name":"mine"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
		DefaultsFn: func() (*Config, error) {
			return &Config{Name: "defaults", Workflow: wf, Tracker: TrackerConfig{Kind: "native"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)

	rec := httptest.NewRecorder()
	m.handleApplyDefaults(rec, httptest.NewRequest(http.MethodPost, "/defaults/apply", nil))
	if rec.Code != http.StatusConflict || m.Config().Name != "mine" {
		t.Fatalf("without replace: code=%d name=%q, want 409 and the saved config kept", rec.Code, m.Config().Name)
	}

	rec = httptest.NewRecorder()
	m.handleApplyDefaults(rec, httptest.NewRequest(http.MethodPost, "/defaults/apply?replace=1", nil))
	if m.Config().Name != "defaults" {
		t.Fatalf("with replace=1: code=%d name=%q, want the defaults saved", rec.Code, m.Config().Name)
	}
	disk, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(disk), `"defaults"`) {
		t.Fatalf("defaults not persisted: %s", disk)
	}
}
