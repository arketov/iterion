package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
	"github.com/arketov/iterion/pkg/dispatcher"
)

// writeProjectBot lays a minimal bundle (manifest.yaml + main.bot, the shape the
// dispatcher's routing tests use) under <project>/bots/<name>.
func writeProjectBot(t *testing.T, project, name string) {
	t.Helper()
	dir := filepath.Join(project, "bots", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.bot"), []byte("agent x:\n  model: \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The preset is built only from the project: no embedded catalogue, no
// fallback workflow, no per-bot snapshot, require_assignee on. A project with
// no bots at all still gets a valid config — its cards wait as "no bot".
func TestBuildDefaultConfig_ProjectBotsOnly(t *testing.T) {
	project := t.TempDir()
	cfg, err := BuildDefaultConfig(project)
	if err != nil {
		t.Fatalf("BuildDefaultConfig: %v", err)
	}
	if cfg.Tracker.Kind != "native" {
		t.Fatalf("tracker kind: %q", cfg.Tracker.Kind)
	}
	if cfg.Server.Port == 0 {
		t.Fatalf("server port should be non-zero in default mode, got %d", cfg.Server.Port)
	}
	if cfg.Workflow != "" {
		t.Fatalf("the preset has no fallback workflow (a card without a bot must not run), got %q", cfg.Workflow)
	}
	if !cfg.RequireAssignee {
		t.Fatal("require_assignee must be on in the preset")
	}
	if len(cfg.AssigneeWorkflows) != 0 || len(cfg.AssigneeDispatch) != 0 {
		t.Fatalf("routing and dispatch vars are discovery-driven: assignee_workflows=%v assignee_dispatch=%v", cfg.AssigneeWorkflows, cfg.AssigneeDispatch)
	}
	want := botregistry.DefaultPaths(project)
	if len(cfg.Bots.Paths) != len(want) {
		t.Fatalf("Bots.Paths = %v, want the project's roots %v", cfg.Bots.Paths, want)
	}
	for i := range want {
		if cfg.Bots.Paths[i] != want[i] {
			t.Fatalf("Bots.Paths[%d] = %q, want %q", i, cfg.Bots.Paths[i], want[i])
		}
	}
	if names := DefaultAssigneeNames(cfg.Bots.Paths); len(names) != 0 {
		t.Fatalf("a project with no bots has no assignees, got %v", names)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the preset must validate with no bots: %v", err)
	}
}

func TestBuildDefaultConfig_DiscoversProjectBots(t *testing.T) {
	project := t.TempDir()
	writeProjectBot(t, project, "zeta")
	writeProjectBot(t, project, "alpha")
	cfg, err := BuildDefaultConfig(project)
	if err != nil {
		t.Fatalf("BuildDefaultConfig: %v", err)
	}
	got := DefaultAssigneeNames(cfg.Bots.Paths)
	want := []string{"alpha", "zeta"}
	if len(got) != len(want) || !sort.StringsAreSorted(got) {
		t.Fatalf("DefaultAssigneeNames = %v, want %v (sorted)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DefaultAssigneeNames = %v, want %v", got, want)
		}
	}
}

func TestBuildDefaultConfig_RejectsEmptyProjectDir(t *testing.T) {
	if _, err := BuildDefaultConfig(""); err == nil {
		t.Fatal("expected an error on an empty project directory: the preset runs the bots of a project")
	}
}

// BuildDefaultConfig wires an after_create hook that seeds a git worktree of
// the host repo into each per-issue workspace. Teardown is deliberately not a
// shell hook: after deleting a clean workspace the dispatcher safely removes
// its exact Git registration instead of running `remove --force` against a
// checkout that may still be committing.
func TestBuildDefaultConfig_SeedHook(t *testing.T) {
	projectDir := t.TempDir()
	cfg, err := BuildDefaultConfig(projectDir)
	if err != nil {
		t.Fatalf("BuildDefaultConfig: %v", err)
	}
	if cfg.Hooks.AfterCreate == nil {
		t.Fatal("expected an after_create hook")
	}
	if cfg.Hooks.BeforeRemove != nil {
		t.Fatalf("expected no destructive before_remove default, got %+v", cfg.Hooks.BeforeRemove)
	}
	if !strings.Contains(cfg.Hooks.AfterCreate.Script, projectDir) {
		t.Fatalf("after_create hook should reference projectDir %q; got %q", projectDir, cfg.Hooks.AfterCreate.Script)
	}
	if !strings.Contains(cfg.Hooks.AfterCreate.Script, "worktree add") {
		t.Fatal("after_create hook should run `git worktree add`")
	}
}

// A preset an earlier version saved routed to the bots the binary embedded:
// <store>/dispatcher/bots/default as the fallback workflow, that folder first
// on bots.paths, and a snapshot of those bots' dispatch vars. The migration
// turns it into the project-only preset and touches nothing the operator wrote.
func TestMigrateLegacyDefaultPreset(t *testing.T) {
	store := t.TempDir()
	botsDir := filepath.Join(store, "dispatcher", "bots")
	project := t.TempDir()
	legacy := func() *dispatcher.Config {
		return &dispatcher.Config{
			Name:             "iterion-default",
			Workflow:         filepath.Join(botsDir, "default"),
			AssigneeDispatch: map[string]dispatcher.DispatchConfig{"feature-dev": {Vars: map[string]string{"task": "{{issue.title}}"}}},
			Tracker:          dispatcher.TrackerConfig{Kind: dispatcher.TrackerKindNative},
		}
	}

	cfg := legacy()
	cfg.Bots.Paths = append([]string{botsDir}, botregistry.DefaultPaths(project)...)
	if !MigrateLegacyDefaultPreset(cfg, store) {
		t.Fatal("the legacy preset must be migrated")
	}
	if cfg.Workflow != "" || !cfg.RequireAssignee || cfg.AssigneeDispatch != nil {
		t.Fatalf("not migrated: workflow=%q require_assignee=%v assignee_dispatch=%v", cfg.Workflow, cfg.RequireAssignee, cfg.AssigneeDispatch)
	}
	for _, p := range cfg.Bots.Paths {
		if filepath.Clean(p) == filepath.Clean(botsDir) {
			t.Fatalf("the extracted catalogue is still on bots.paths: %v", cfg.Bots.Paths)
		}
	}
	if len(cfg.Bots.Paths) != len(botregistry.DefaultPaths(project)) {
		t.Fatalf("the project's roots must stay: %v", cfg.Bots.Paths)
	}

	// An operator's own config is left alone: another name, or a workflow that
	// is not the extracted default.
	own := legacy()
	own.Name = "mine"
	if MigrateLegacyDefaultPreset(own, store) || own.Workflow == "" {
		t.Fatal("a config the operator named must not be migrated")
	}
	edited := legacy()
	edited.Workflow = filepath.Join(project, "bots", "triage", "main.bot")
	if MigrateLegacyDefaultPreset(edited, store) || edited.Workflow == "" {
		t.Fatal("a config with the operator's own workflow must not be migrated")
	}
	if MigrateLegacyDefaultPreset(nil, store) || MigrateLegacyDefaultPreset(legacy(), "") {
		t.Fatal("nil config / empty store must be a no-op")
	}
}

// The workspace is seeded from the host repository, whose hooks directory a
// run can write: the seeding's `git worktree add` runs none.
func TestWorkspaceSeedingRunsNoRepositoryHook(t *testing.T) {
	script := workspaceSeedScript("/srv/project")
	if !strings.Contains(script, "git -C \"$PROJECT_DIR\" -c core.hooksPath=/dev/null -c core.fsmonitor=false worktree add") {
		t.Fatalf("the workspace seeding runs the host repository's hooks:\n%s", script)
	}
}

func TestWorkspaceSeedPowerShellQuotesProjectDir(t *testing.T) {
	got := workspaceSeedPowerShell(`C:\Users\O'Brien\proj`)
	if !strings.Contains(got, `$projectDir = 'C:\Users\O''Brien\proj'`) {
		t.Fatalf("project dir not single-quoted and escaped:\n%s", got)
	}
	for _, want := range []string{"worktree add --detach", "core.hooksPath=/dev/null", "ITERION_WORKSPACE", "Copy-Item"} {
		if !strings.Contains(got, want) {
			t.Errorf("seed script lacks %q", want)
		}
	}
}
