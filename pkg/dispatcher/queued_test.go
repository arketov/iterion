package dispatcher

import (
	"context"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// fullSlotsDispatcher is a dispatcher whose global cap (1) is taken by another
// card, with require_assignee on and the ticket router off.
func fullSlotsDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	c := routerTestDispatcher(t, newFakeTracker())
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.RequireAssignee = true
	cfg.Agent.MaxConcurrent = 1
	c.cfg.Store(&cfg)
	c.state.running["busy"] = &runningEntry{IssueID: "busy"}
	return c
}

// A refusal recorded before a bot was assigned must give way to "queued" while
// every slot is busy: dispatch() is not reached, so the poll has to re-judge it.
func TestCandidates_StaleNoAssigneeReplacedByQueuedWhenSlotsFull(t *testing.T) {
	c := fullSlotsDispatcher(t)
	ctx := context.Background()

	iss := tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "ready"}
	cmdCandidates{issues: []tracker.Issue{iss}}.apply(c, ctx)
	if sk := c.state.dispatchSkips["i1"]; sk.Kind != DispatchSkipNoAssignee {
		t.Fatalf("unassigned card: want no_assignee skip, got %+v", sk)
	}

	iss.Bot = "bugfix"
	cmdCandidates{issues: []tracker.Issue{iss}}.apply(c, ctx)
	sk, ok := c.state.dispatchSkips["i1"]
	if !ok || sk.Kind != DispatchSkipQueued {
		t.Fatalf("card with a bot: want queued skip, got %+v ok=%v", sk, ok)
	}
	if !strings.Contains(sk.Reason, "waiting for a slot") || !strings.Contains(sk.Reason, "1/1") {
		t.Errorf("reason = %q, want it to say waiting for a slot (… 1/1)", sk.Reason)
	}
	if sk.Bot != "bugfix" {
		t.Errorf("skip bot = %q, want bugfix", sk.Bot)
	}
	if len(c.state.running) != 1 {
		t.Errorf("running = %d, want 1 (the queued card must not start)", len(c.state.running))
	}
}

func TestCandidates_QueuedReasonNamesTheStateCap(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.Agent.MaxConcurrent = 4
	cfg.Agent.MaxConcurrentByState = map[string]int{"ready": 1}
	c.cfg.Store(&cfg)
	c.state.running["busy"] = &runningEntry{IssueID: "busy"}
	c.state.slotsByState["ready"] = 1

	cmdCandidates{issues: []tracker.Issue{{ID: "i1", Identifier: "T-1", WorkflowState: "ready", Bot: "bugfix"}}}.apply(c, context.Background())
	sk := c.state.dispatchSkips["i1"]
	if sk.Kind != DispatchSkipQueued || !strings.Contains(sk.Reason, "ready 1/1") {
		t.Fatalf("want queued skip naming ready 1/1, got %+v", sk)
	}
}

// The router gives an unassigned card its bot inside dispatch(); at the cap it
// must not be labelled no_assignee in the meantime.
func TestCandidates_RouterUnassignedCardIsNotLabelledAtCap(t *testing.T) {
	c := fullSlotsDispatcher(t)
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{Enabled: true}
	c.cfg.Store(&cfg)

	cmdCandidates{issues: []tracker.Issue{{ID: "i1", Identifier: "T-1", WorkflowState: "ready"}}}.apply(c, context.Background())
	if sk, ok := c.state.dispatchSkips["i1"]; ok {
		t.Fatalf("router-unassigned card got a skip at the cap: %+v", sk)
	}
}

func TestCandidates_OtherRefusalsSurviveQueueing(t *testing.T) {
	for _, kind := range []string{DispatchSkipSourceChanged, ""} {
		c := fullSlotsDispatcher(t)
		iss := tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "ready", Bot: "bugfix"}
		c.recordDispatchSkipKind(iss, kind, "own reason")

		cmdCandidates{issues: []tracker.Issue{iss}}.apply(c, context.Background())
		sk := c.state.dispatchSkips["i1"]
		if sk.Kind != kind || sk.Reason != "own reason" {
			t.Errorf("kind %q: refusal overwritten by queueing: %+v", kind, sk)
		}
	}
}

// Discovery must run at the global cap, otherwise apply never re-judges the
// waiting cards.
func TestTick_DiscoversWhileSlotsFull(t *testing.T) {
	c := fullSlotsDispatcher(t)
	// The running card must exist on the tracker, or refreshRunningStates reaps
	// it as "disappeared" and frees the slot before the cap check.
	c.tracker.(*fakeTracker).add(tracker.Issue{ID: "busy", Identifier: "T-0", WorkflowState: "in_progress"})
	c.state.running["busy"] = &runningEntry{IssueID: "busy", Identifier: "T-0", WorkflowState: "in_progress"}

	c.tick(context.Background())
	if got, want := len(c.state.running), c.cfg.Load().Agent.MaxConcurrent; got != want {
		t.Fatalf("running = %d, want %d: the cap must still be full when tick checks it", got, want)
	}
	if !c.state.discoveryInFlight {
		t.Fatal("tick must start discovery while every slot is busy")
	}
	c.workersWG.Wait()
}
