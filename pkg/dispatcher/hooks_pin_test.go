package dispatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Whatever the host, a hook that already names its shell, one whose text is
// PowerShell, and an absent hook are left alone: only an sh script kept on sh
// by the text heuristic is ever pinned.
func TestPinHeuristicShells_LeavesEverythingElseAlone(t *testing.T) {
	t.Setenv("ITERION_SHELL", "")
	cfg := &Config{Hooks: Hooks{
		AfterCreate: &Hook{Script: "set -e\necho x", Shell: "pwsh"},
		BeforeRun:   &Hook{Script: "Write-Output hi"},
		AfterRun:    &Hook{Path: "./scripts/after.sh"},
		// BeforeRemove unset.
	}}
	pinned := PinHeuristicShells(cfg)
	for _, name := range pinned {
		if name == "after_create" || name == "after_run" || name == "before_remove" {
			t.Errorf("hook %s must not be pinned", name)
		}
	}
	if cfg.Hooks.AfterCreate.Shell != "pwsh" {
		t.Errorf("an explicit shell was replaced: %q", cfg.Hooks.AfterCreate.Shell)
	}
	if cfg.Hooks.BeforeRun.Shell != "" && cfg.Hooks.BeforeRun.Shell != "sh" && cfg.Hooks.BeforeRun.Shell != "bash" {
		t.Errorf("unexpected shell %q for a PowerShell-looking hook", cfg.Hooks.BeforeRun.Shell)
	}
	if got := PinHeuristicShells(nil); got != nil {
		t.Errorf("PinHeuristicShells(nil) = %v", got)
	}
}

// ITERION_SHELL is an explicit, process-wide choice: it is never written into
// the saved config.
func TestPinHeuristicShells_IgnoresHooksChosenByTheEnvironment(t *testing.T) {
	t.Setenv("ITERION_SHELL", "sh")
	cfg := &Config{Hooks: Hooks{BeforeRun: &Hook{Script: "set -e\necho x"}}}
	if pinned := PinHeuristicShells(cfg); len(pinned) != 0 || cfg.Hooks.BeforeRun.Shell != "" {
		t.Fatalf("pinned %v, shell %q: a hook the environment picks the shell for must stay as it is", pinned, cfg.Hooks.BeforeRun.Shell)
	}
}

// patchHookFields rewrites the named fields of the named hooks and nothing
// else in the file.
func TestPatchHookFieldsTouchesOnlyTheNamedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatcher.json")
	body := `{"workflow":"w.bot","workspace":{"root":"relroot"},"hooks":{"after_create":{"script":"echo a"},"before_run":{"script":"set -e\necho b"},"after_run":{"script":"echo c"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := patchHookFields(path, map[string]map[string]string{
		"before_run": {"shell": "sh"},
		"after_run":  {"shell": ""}, // empty values are not written
	})
	if err != nil {
		t.Fatalf("patchHookFields: %v", err)
	}
	disk, _ := os.ReadFile(path)
	out := string(disk)
	if !strings.Contains(out, `"shell": "sh"`) {
		t.Errorf("shell not written:\n%s", out)
	}
	if strings.Count(out, `"shell"`) != 1 {
		t.Errorf("an empty value or another hook was written:\n%s", out)
	}
	if !strings.Contains(out, `"root": "relroot"`) || !strings.Contains(out, `"script": "echo a"`) {
		t.Errorf("the rest of the file changed:\n%s", out)
	}
	if err := patchHookFields(path, map[string]map[string]string{"before_remove": {"shell": "sh"}}); err == nil {
		t.Error("patching a hook the file does not have must fail, not silently add one")
	}
}
