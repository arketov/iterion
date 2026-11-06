package dispatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
)

// botsConfig is a bots: block with the given discovery roots.
func botsConfig(paths ...string) botregistry.Config {
	return botregistry.Config{Paths: paths}
}

// With require_assignee the fallback workflow is optional: the dispatcher then
// runs only bots it can route to. Without it a card with no bot has nothing to
// run, so the workflow stays required.
func TestConfigWorkflowOptionalWithRequireAssignee(t *testing.T) {
	botsDir := t.TempDir()
	cfg := &Config{
		RequireAssignee: true,
		Tracker:         TrackerConfig{Kind: TrackerKindNative},
		Bots:            botsConfig(botsDir),
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("require_assignee with bots.paths and no workflow must validate: %v", err)
	}

	cfg.RequireAssignee = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "workflow is required") {
		t.Fatalf("without require_assignee the workflow is required, got %v", err)
	}

	cfg.RequireAssignee = true
	cfg.Bots.Paths = nil
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "nothing can run") {
		t.Fatalf("no workflow, no assignee_workflows and no bots.paths: nothing can run, got %v", err)
	}
}

// Resolved builds the copy the dispatcher runs and leaves the saved form alone:
// the token reference and the relative path stay as the operator wrote them.
func TestConfigResolvedLeavesRawUntouched(t *testing.T) {
	t.Setenv("DISPATCH_TEST_GH_TOKEN", "s3cret")
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "flow.bot"), []byte("workflow x: x -> done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := &Config{
		Workflow: "flow.bot",
		Tracker: TrackerConfig{Kind: TrackerKindGitHub, GitHub: &GitHubTrackerConfig{
			Repo: "o/r", Token: "$DISPATCH_TEST_GH_TOKEN",
		}},
		Agent: AgentConfig{RunningState: "none", MaxAttempts: -1},
	}

	rt, err := raw.Resolved(base, nil)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if rt.Tracker.GitHub.Token != "s3cret" {
		t.Fatalf("runtime token = %q, want the expanded value", rt.Tracker.GitHub.Token)
	}
	if rt.Workflow != filepath.Join(base, "flow.bot") {
		t.Fatalf("runtime workflow = %q, want it resolved against %q", rt.Workflow, base)
	}
	if rt.Agent.RunningState != "" || rt.Agent.MaxAttempts != 0 {
		t.Fatalf("runtime must read the escape hatches: running_state=%q max_attempts=%d", rt.Agent.RunningState, rt.Agent.MaxAttempts)
	}
	// The raw form is what gets saved: nothing in it changed.
	if raw.Tracker.GitHub.Token != "$DISPATCH_TEST_GH_TOKEN" || raw.Workflow != "flow.bot" {
		t.Fatalf("Resolved modified the saved form: token=%q workflow=%q", raw.Tracker.GitHub.Token, raw.Workflow)
	}
	if raw.Agent.RunningState != "none" || raw.Agent.MaxAttempts != -1 {
		t.Fatalf("Resolved applied defaults to the saved form: %+v", raw.Agent)
	}
}

// bots.paths entries are resolved against the same base as the workflow, and an
// empty list takes the default roots.
func TestConfigBotsPathsResolvedAgainstBase(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "mybots"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := &Config{
		RequireAssignee: true,
		Tracker:         TrackerConfig{Kind: TrackerKindNative},
		Bots:            botsConfig("mybots"),
	}
	rt, err := raw.Resolved(base, nil)
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if len(rt.Bots.Paths) != 1 || rt.Bots.Paths[0] != filepath.Join(base, "mybots") {
		t.Fatalf("bots.paths = %v, want it resolved against the base", rt.Bots.Paths)
	}
	if raw.Bots.Paths[0] != "mybots" {
		t.Fatalf("the saved bots.paths changed: %v", raw.Bots.Paths)
	}

	empty := &Config{RequireAssignee: true, Tracker: TrackerConfig{Kind: TrackerKindNative}}
	rt, err = empty.Resolved(base, []string{filepath.Join(base, "bots")})
	if err != nil || len(rt.Bots.Paths) != 1 {
		t.Fatalf("an empty bots.paths takes the defaults: paths=%v err=%v", rt.Bots.Paths, err)
	}
}

// LoadRaw keeps `$ENV` and `~` as written and makes plain relative paths
// absolute against the YAML's own folder, so what a YAML wrote relative to
// itself still points at the same file once it is saved into the store.
func TestLoadRawAbsolutizesOnlyPlainRelPaths(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "iterion.dispatcher.yaml")
	body := "workflow: ./flow.bot\n" +
		"tracker:\n  kind: github\n  github:\n    repo: o/r\n    token: $DISPATCH_TEST_TOKEN\n" +
		"workspace:\n  root: ~/ws\n" +
		"bots:\n  paths:\n    - ./bots\n    - $HOME/shared-bots\n"
	if err := os.WriteFile(yamlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := LoadRaw(yamlPath)
	if err != nil {
		t.Fatalf("LoadRaw: %v", err)
	}
	if raw.Workflow != filepath.Join(dir, "flow.bot") {
		t.Fatalf("workflow = %q, want it made absolute against the YAML's folder", raw.Workflow)
	}
	if raw.Tracker.GitHub.Token != "$DISPATCH_TEST_TOKEN" {
		t.Fatalf("token = %q, want the reference kept", raw.Tracker.GitHub.Token)
	}
	if raw.Workspace.Root != "~/ws" {
		t.Fatalf("workspace.root = %q, want ~ kept", raw.Workspace.Root)
	}
	if raw.Bots.Paths[0] != filepath.Join(dir, "bots") || raw.Bots.Paths[1] != "$HOME/shared-bots" {
		t.Fatalf("bots.paths = %v", raw.Bots.Paths)
	}
}
