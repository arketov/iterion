package dispatcher

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// A failed after_create is written on the card itself, survives the dispatcher
// being stopped (the owner's "I press Stop and the error is gone" case), and
// no phantom run is stamped.
func TestFailedHookIsPersistedOnTheCard(t *testing.T) {
	dir := t.TempDir()
	ns, err := native.NewStore(filepath.Join(dir, "dispatcher"))
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	iss, err := ns.Create(native.Issue{Title: "hook fails", State: "ready", Assignee: "feature-dev"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Name:      "test",
		Workflow:  t.TempDir() + "/fake.bot",
		Tracker:   TrackerConfig{Kind: TrackerKindNative},
		Polling:   PollingConfig{IntervalMS: 30},
		Agent:     AgentConfig{MaxConcurrent: 2, MaxRetryBackoffMS: 60000, RunningState: "in_progress"},
		Workspace: WorkspaceConfig{Root: filepath.Join(dir, "ws")},
		Hooks:     Hooks{AfterCreate: &Hook{Script: "echo diag >&2; exit 3"}},
	}
	cfg.applyDefaults()
	ws, err := NewWorkspaces(filepath.Join(dir, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{
		Config: cfg, Tracker: native.NewAdapter(ns), Runner: &StubRunner{}, Workspaces: ws,
		Logger:     iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test", StoreDir: filepath.Join(dir, "store"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)

	deadline := time.Now().Add(15 * time.Second)
	for {
		got, _ := ns.Get(iss.ID)
		if got != nil && len(got.LaunchFailures) > 0 {
			break
		}
		if time.Now().After(deadline) {
			c.Stop()
			t.Fatal("no launch failure recorded on the card")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.Stop() // the in-memory retry queue is gone now

	got, _ := ns.Get(iss.ID)
	f := got.LaunchFailures[0]
	if f.Hook != "after_create" || f.Error == "" || f.RunID == "" || f.Attempt != 1 || f.At.IsZero() {
		t.Fatalf("incomplete record: %+v", f)
	}
	// An exit code, when the shell ran, is the hook's own.
	if f.ExitCode != nil && *f.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", *f.ExitCode)
	}
	if got.LastRunID != "" || len(got.Runs) != 0 {
		t.Fatalf("a run that never started was recorded: last=%q runs=%+v", got.LastRunID, got.Runs)
	}
}

// A card whose after_create keeps failing is given up on, and the give-up
// leaves it in the default FailedState ("blocked") with its launch failures
// intact: that is the card the studio's "Did not start" panel must keep listing
// (see studio/src/lib/launchFailure.test.ts, which pins the filter on this state).
func TestFailedHookGiveUpKeepsLaunchFailuresOnTheBlockedCard(t *testing.T) {
	dir := t.TempDir()
	ns, err := native.NewStore(filepath.Join(dir, "dispatcher"))
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	t.Cleanup(func() { _ = ns.Close() })
	iss, err := ns.Create(native.Issue{Title: "hook fails to give-up", State: native.StateReady, Assignee: "feature-dev"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Name:      "test",
		Workflow:  t.TempDir() + "/fake.bot",
		Tracker:   TrackerConfig{Kind: TrackerKindNative},
		Polling:   PollingConfig{IntervalMS: 30},
		Agent:     AgentConfig{MaxConcurrent: 2, MaxAttempts: 1, MaxRetryBackoffMS: 60000, RunningState: "in_progress"},
		Workspace: WorkspaceConfig{Root: filepath.Join(dir, "ws")},
		Hooks:     Hooks{AfterCreate: &Hook{Script: "echo diag >&2; exit 3"}},
	}
	cfg.applyDefaults()
	if cfg.Agent.FailedState != native.StateBlocked {
		t.Fatalf("default FailedState = %q, want %q", cfg.Agent.FailedState, native.StateBlocked)
	}
	ws, err := NewWorkspaces(filepath.Join(dir, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{
		Config: cfg, Tracker: native.NewAdapter(ns), Runner: &StubRunner{}, Workspaces: ws,
		Logger:     iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test", StoreDir: filepath.Join(dir, "store"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)

	// Wait on the give-up stamp, written after the state move, not on the state alone.
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, _ := ns.Get(iss.ID)
		if got != nil && got.State == native.StateBlocked && got.GaveUp != nil {
			break
		}
		if time.Now().After(deadline) {
			c.Stop()
			got, _ := ns.Get(iss.ID)
			t.Fatalf("no give-up: state=%q gaveUp=%+v failures=%d", got.State, got.GaveUp, len(got.LaunchFailures))
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.Stop()

	got, _ := ns.Get(iss.ID)
	if got.State != native.StateBlocked {
		t.Fatalf("state = %q, want %q", got.State, native.StateBlocked)
	}
	if len(got.LaunchFailures) != 1 {
		t.Fatalf("launch failures = %d, want 1: %+v", len(got.LaunchFailures), got.LaunchFailures)
	}
	if f := got.LaunchFailures[0]; f.Hook != "after_create" || f.Error == "" || f.RunID == "" || f.Attempt != 1 {
		t.Fatalf("incomplete record: %+v", f)
	}
	if got.GaveUp.State != native.StateBlocked || got.GaveUp.Attempts != 1 {
		t.Fatalf("give-up stamp = %+v, want state %q after 1 attempt", got.GaveUp, native.StateBlocked)
	}
	if got.LastRunID != "" || len(got.Runs) != 0 {
		t.Fatalf("a run that never started was recorded: last=%q runs=%+v", got.LastRunID, got.Runs)
	}
}
