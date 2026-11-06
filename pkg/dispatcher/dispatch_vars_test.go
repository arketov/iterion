package dispatcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

func writeBotWithVars(t *testing.T, botsDir, name, vars string) {
	t.Helper()
	manifest := "name: " + name + "\n"
	if vars != "" {
		manifest += "dispatch_vars:\n" + vars
	}
	writeDispatcherBot(t, filepath.Join(botsDir, name, "manifest.yaml"), manifest)
	writeDispatcherBot(t, filepath.Join(botsDir, name, "main.bot"), "agent x:\n  model: \"test\"\n")
}

func varsDispatcher(t *testing.T, botsDir string) *Dispatcher {
	t.Helper()
	d := newMinimalDispatcher(t)
	d.runner = &RoutingRunner{
		BotsPaths: []string{botsDir},
		compile:   func(string) (Runner, error) { return &recordingRunner{name: "dynamic"}, nil },
	}
	return d
}

// A bot created AFTER the dispatcher was configured still gets its own input
// vars: they are read from its manifest at dispatch time, not captured once in
// dispatcher.json.
func TestBuildSpec_ManifestDispatchVarsReadLive(t *testing.T) {
	botsDir := filepath.Join(t.TempDir(), "bots")
	d := varsDispatcher(t, botsDir)
	cfg := &Config{
		Workflow: "/tmp/default.bot",
		Dispatch: DispatchConfig{Vars: map[string]string{"issue_title": "{{issue.title}}"}},
	}
	iss := tracker.Issue{ID: "i1", Title: "Fix it", Body: "details", Assignee: "late-bot"}

	// Not created yet: the global vars apply.
	spec := d.buildSpec(cfg, iss, "r1", "/tmp/ws", 0, nil)
	if _, ok := spec.Vars["task"]; ok || spec.Vars["issue_title"] != "Fix it" {
		t.Fatalf("before the bot exists: want the global vars, got %v", spec.Vars)
	}

	// Created now, with no config change and no restart.
	writeBotWithVars(t, botsDir, "late-bot", "  task: \"{{issue.title}} / {{issue.body}}\"\n")
	spec = d.buildSpec(cfg, iss, "r2", "/tmp/ws", 0, nil)
	if spec.Vars["task"] != "Fix it / details" {
		t.Fatalf("manifest dispatch_vars not applied to a bot created later: %v", spec.Vars)
	}
	if _, ok := spec.Vars["issue_title"]; ok {
		t.Fatalf("a bot's own vars replace the global ones wholesale, got %v", spec.Vars)
	}

	// Edited later still: the next card sees the edit.
	if err := os.WriteFile(filepath.Join(botsDir, "late-bot", "manifest.yaml"),
		[]byte("name: late-bot\ndispatch_vars:\n  prompt: \"{{issue.title}}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec = d.buildSpec(cfg, iss, "r3", "/tmp/ws", 0, nil)
	if _, stale := spec.Vars["task"]; spec.Vars["prompt"] != "Fix it" || stale {
		t.Fatalf("an edited manifest must be re-read: %v", spec.Vars)
	}
}

// Routing forgives case and `_`/`-`; the vars lookup now does too, so the card
// that routes to `my-helper` as `My_Helper` also gets `my-helper`'s vars.
func TestBuildSpec_DispatchVarsLookupIsNormalizedLikeRouting(t *testing.T) {
	botsDir := filepath.Join(t.TempDir(), "bots")
	writeBotWithVars(t, botsDir, "my-helper", "  task: \"{{issue.title}}\"\n")
	d := varsDispatcher(t, botsDir)
	cfg := &Config{Workflow: "/tmp/default.bot"}

	spec := d.buildSpec(cfg, tracker.Issue{ID: "i1", Title: "T", Assignee: "My_Helper"}, "r1", "/tmp/ws", 0, nil)
	if spec.Vars["task"] != "T" {
		t.Fatalf("My_Helper routes to my-helper but its vars were not found: %v", spec.Vars)
	}

	// An explicit assignee_dispatch entry is found by the same tolerant name.
	cfg.AssigneeDispatch = map[string]DispatchConfig{"my_helper": {Vars: map[string]string{"task": "explicit {{issue.title}}"}}}
	spec = d.buildSpec(cfg, tracker.Issue{ID: "i2", Title: "T", Assignee: "My-Helper"}, "r2", "/tmp/ws", 0, nil)
	if spec.Vars["task"] != "explicit T" {
		t.Fatalf("an explicit assignee_dispatch entry must win over the manifest: %v", spec.Vars)
	}
}

// A static assignee_workflows route runs a workflow the registry does not name,
// so a same-named bot's manifest vars are not its vars.
func TestBotDispatchVarsIgnoresStaticRoute(t *testing.T) {
	botsDir := filepath.Join(t.TempDir(), "bots")
	writeBotWithVars(t, botsDir, "my-helper", "  task: \"{{issue.title}}\"\n")
	rr := &RoutingRunner{
		BotsPaths:  []string{botsDir},
		ByAssignee: map[string]Runner{"my-helper": &recordingRunner{name: "static"}},
	}
	if vars, ok := rr.BotDispatchVars("my-helper"); ok {
		t.Fatalf("a static route must not take the manifest's vars, got %v", vars)
	}
	rr.ByAssignee = nil
	if vars, ok := rr.BotDispatchVars("my-helper"); !ok || vars["task"] == "" {
		t.Fatalf("a discovered bot's manifest vars must be returned, got %v ok=%v", vars, ok)
	}
	if _, ok := rr.BotDispatchVars("ghost"); ok {
		t.Fatal("an unknown bot has no vars")
	}
}
