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
