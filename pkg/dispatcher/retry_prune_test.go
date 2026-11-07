package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// launchFakeTracker adds the optional tracker.LaunchStateLister capability to
// the plain fakeTracker (which must not gain it: that would switch on the
// launch-state branches of every reaper test).
type launchFakeTracker struct{ *fakeTracker }

func (launchFakeTracker) LaunchStates() ([]string, error) { return []string{"ready"}, nil }

// queuedRetryFor puts a retry entry with a live timer for card id (in the given
// tracker state, "" = the card does not exist) into a dispatcher built on a
// launch-aware tracker.
func queuedRetryFor(t *testing.T, state string, withCapability bool) (*Dispatcher, *fakeTracker, *retryEntry) {
	t.Helper()
	ft := newFakeTracker()
	if state != "" {
		ft.issues["i1"] = &tracker.Issue{ID: "i1", Identifier: "T-i1", Title: "queued", WorkflowState: state}
	}
	var tr tracker.Tracker = ft
	if withCapability {
		tr = launchFakeTracker{ft}
	}
	c := routerTestDispatcher(t, tr)
	e := &retryEntry{
		IssueID: "i1", Identifier: "T-i1", Attempt: 2,
		DueAt: time.Now().Add(time.Hour), PrevRunID: "run-prev",
		Timer: time.AfterFunc(time.Hour, func() {}),
	}
	c.state.retries["i1"] = e
	t.Cleanup(func() { e.Timer.Stop() })
	return c, ft, e
}

// The card from the bug report: a failing hook queued a retry, then the card
// was dragged to done. Nothing re-dispatches it, so the row used to stay.
func TestPruneRetries_DropsEntryWhenCardLeftLaunchColumns(t *testing.T) {
	c, _, e := queuedRetryFor(t, "done", true)
	c.pruneRetriesOutOfLaunch(context.Background())

	if _, ok := c.state.retries["i1"]; ok {
		t.Fatal("the retry entry of a card moved to done must be dropped")
	}
	if e.Timer.Stop() {
		t.Error("the entry's timer must be stopped when the entry is dropped")
	}
}

func TestPruneRetries_KeepsEntryWhileCardStillInLaunchColumn(t *testing.T) {
	c, _, _ := queuedRetryFor(t, "ready", true)
	c.pruneRetriesOutOfLaunch(context.Background())
	if _, ok := c.state.retries["i1"]; !ok {
		t.Fatal("a card still in a launch column must keep its retry entry")
	}
}

func TestPruneRetries_KeepsEntryInRunningState(t *testing.T) {
	c, _, _ := queuedRetryFor(t, c0RunningState(t), true)
	c.pruneRetriesOutOfLaunch(context.Background())
	if _, ok := c.state.retries["i1"]; !ok {
		t.Fatal("a card in agent.running_state is not 'moved away' (finish revert window)")
	}
}

func c0RunningState(t *testing.T) string {
	t.Helper()
	cfg := &Config{}
	cfg.applyDefaults()
	if cfg.Agent.RunningState == "" {
		t.Fatal("expected a default running state")
	}
	return cfg.Agent.RunningState
}

func TestPruneRetries_SkipsRemovingEntry(t *testing.T) {
	c, _, e := queuedRetryFor(t, "blocked", true)
	e.Removing = true
	c.pruneRetriesOutOfLaunch(context.Background())
	if _, ok := c.state.retries["i1"]; !ok {
		t.Fatal("an entry held by an operator removal must not be touched")
	}
}

func TestPruneRetries_KeepsEntryWhenCardAbsentFromRefresh(t *testing.T) {
	c, _, _ := queuedRetryFor(t, "", true)
	c.pruneRetriesOutOfLaunch(context.Background())
	if _, ok := c.state.retries["i1"]; !ok {
		t.Fatal("absence from RefreshStates is indistinguishable from a read failure: keep the entry")
	}
}

func TestPruneRetries_NoCapabilityTracker(t *testing.T) {
	c, _, _ := queuedRetryFor(t, "done", false)
	c.pruneRetriesOutOfLaunch(context.Background())
	if _, ok := c.state.retries["i1"]; !ok {
		t.Fatal("a tracker that cannot list its launch columns must leave entries alone")
	}
}

func TestTick_PrunesStaleRetryWhilePaused(t *testing.T) {
	c, ft, _ := queuedRetryFor(t, "done", true)
	c.paused.Store(true)
	c.tick(context.Background())

	if _, ok := c.state.retries["i1"]; ok {
		t.Fatal("a paused tick must still drop the retry entry of a card moved out of the launch columns")
	}
	if n := ft.listCalls.Load(); n != 0 {
		t.Errorf("a paused tick must not run discovery, ListCandidates called %d times", n)
	}
}
