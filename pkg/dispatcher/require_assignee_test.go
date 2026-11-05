package dispatcher

import (
	"context"
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
