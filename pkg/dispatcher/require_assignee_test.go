package dispatcher

import (
	"bytes"
	"path/filepath"
	"time"

	"context"
	iterlog "github.com/arketov/iterion/pkg/log"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// With require_assignee on, a card with neither assignee nor bot is not
// claimed: it is reported as a "no_assignee" skip and no slot is taken.
func TestDispatchRequireAssigneeSkipsUnassigned(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.RequireAssignee = true
	c.cfg.Store(&cfg)

	iss := tracker.Issue{ID: "i1", Identifier: "T-1"}
	c.dispatch(context.Background(), iss)

	if len(c.state.running) != 0 {
		t.Fatalf("unassigned card must not start a run, running=%d", len(c.state.running))
	}
	sk, ok := c.state.dispatchSkips["i1"]
	if !ok || sk.Kind != DispatchSkipNoAssignee || sk.Reason == "" {
		t.Fatalf("want no_assignee skip, got %+v ok=%v", sk, ok)
	}
}

// An explicit assignee is not touched by the guard (it falls through to the
// normal bot checks, so no no_assignee skip is recorded).
func TestDispatchRequireAssigneeLeavesAssignedAlone(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.RequireAssignee = true
	c.cfg.Store(&cfg)

	c.dispatch(context.Background(), tracker.Issue{ID: "i2", Identifier: "T-2", Assignee: "feature-dev"})
	if sk, ok := c.state.dispatchSkips["i2"]; ok && sk.Kind == DispatchSkipNoAssignee {
		t.Fatalf("assigned card wrongly skipped as no_assignee: %+v", sk)
	}
}

// A run whose after_create hook failed never started: stamping it would put a
// run id with no run.json in the card's history.
func TestStampLastRunSkipsNeverStartedRun(t *testing.T) {
	lt := newLastRunTracker()
	c := routerTestDispatcher(t, lt)
	r := &runningEntry{IssueID: "i1", Identifier: "T-1", RunID: "run-ghost", WorkspacePath: t.TempDir()}
	r.neverStarted.Store(true)
	c.stampLastRun("i1", r)
	if n := len(lt.lastRunCalls()); n != 0 {
		t.Fatalf("SetLastRun calls = %d, want 0 for a never-started run", n)
	}
	r2 := &runningEntry{IssueID: "i1", Identifier: "T-1", RunID: "run-real", WorkspacePath: t.TempDir()}
	c.stampLastRun("i1", r2)
	if n := len(lt.lastRunCalls()); n != 1 {
		t.Fatalf("SetLastRun calls = %d, want 1 for a started run", n)
	}
}

// End to end: a fresh run whose after_create hook fails never reaches the
// card's history (no SetLastRun at claim time), while the retry queue row
// names the hook.
func TestFailedAfterCreateLeavesNoPhantomRun(t *testing.T) {
	lt := newLastRunTracker()
	lt.add(tracker.Issue{ID: "fake:ghost", Identifier: "fake#ghost", Title: "go", WorkflowState: "ready"})

	dir := t.TempDir()
	cfg := &Config{
		Name:      "test",
		Workflow:  t.TempDir() + "/fake.bot",
		Tracker:   TrackerConfig{Kind: "fake"},
		Polling:   PollingConfig{IntervalMS: 30},
		Agent:     AgentConfig{MaxConcurrent: 4, MaxRetryBackoffMS: 60000, RunningState: "in_progress"},
		Workspace: WorkspaceConfig{Root: filepath.Join(dir, "ws")},
		Stall:     StallConfig{TimeoutMS: 0},
		Hooks:     Hooks{AfterCreate: &Hook{Script: "exit 3"}},
	}
	cfg.applyDefaults()
	ws, err := NewWorkspaces(filepath.Join(dir, "ws"))
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	c, err := New(Options{
		Config: cfg, Tracker: lt, Runner: &StubRunner{}, Workspaces: ws,
		Logger:     iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test", StoreDir: filepath.Join(dir, "store"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if snap := c.Snapshot(); len(snap.Retries) == 1 {
			if snap.Retries[0].Hook != "after_create" {
				t.Fatalf("retry row hook = %q, want after_create", snap.Retries[0].Hook)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no retry queued after the failed hook")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls := lt.lastRunCalls(); len(calls) != 0 {
		t.Fatalf("a run that never started was recorded on the card: %+v", calls)
	}
}
