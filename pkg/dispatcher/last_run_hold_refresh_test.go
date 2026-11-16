package dispatcher

import (
	"context"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/store"
)

// holdLastRunTracker is a fakeTracker that serves the LastRunForIssue seam the
// last-run hold reads through.
type holdLastRunTracker struct {
	*fakeTracker
	lastRun map[string]string
}

func (l *holdLastRunTracker) LastRunForIssue(id string) (string, error) { return l.lastRun[id], nil }

// lastRunHoldDispatcher is a full-slots dispatcher (global cap 1, taken by
// another card) whose tracker points the card "i1" at last run "run-1".
func lastRunHoldDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	c := fullSlotsDispatcher(t)
	c.tracker = &holdLastRunTracker{fakeTracker: newFakeTracker(), lastRun: map[string]string{"i1": "run-1"}}
	c.storeDir = t.TempDir()
	return c
}

func setRunStatus(t *testing.T, storeDir, runID string, status store.RunStatus) {
	t.Helper()
	s, err := store.New(storeDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	r, err := s.LoadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	r.Status = status
	if err := s.SaveRun(context.Background(), r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

// The "returned the card to review" hold belongs to the (column, bot) the card
// had when it was recorded. Once the card sits in merge under another bot the
// hold no longer applies, and with every slot busy noteQueued is the only
// code that sees the card.
func TestNoteQueued_StaleRunFinishedHoldClearedAfterColumnMove(t *testing.T) {
	c := lastRunHoldDispatcher(t)
	writeRunDoc(t, c.storeDir, "run-1", store.RunStatusFinished, nil) // workflow "wf"

	inReview := tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "review", Bot: "wf", StateReason: tracker.ReasonRunFinished}
	if !c.lastRunHoldBeforeClaim(inReview) {
		t.Fatal("precondition: the card returned to review by its own bot must be held")
	}
	if sk := c.state.dispatchSkips["i1"]; sk.Kind != DispatchSkipRunFinished || !strings.Contains(sk.Reason, "returned the card") {
		t.Fatalf("precondition: want a run_finished hold, got %+v", sk)
	}

	inMerge := tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "merge", Bot: "merger", StateReason: tracker.ReasonRunFinished}
	cmdCandidates{issues: []tracker.Issue{inMerge}}.apply(c, context.Background())

	sk, ok := c.state.dispatchSkips["i1"]
	if !ok || sk.Kind != DispatchSkipQueued {
		t.Fatalf("card moved to merge under another bot: want a queued skip, got %+v ok=%v", sk, ok)
	}
	if strings.Contains(sk.Reason, "returned the card") {
		t.Errorf("stale review hold survived the column move: %q", sk.Reason)
	}
	if sk.Bot != "merger" {
		t.Errorf("skip bot = %q, want merger", sk.Bot)
	}
}

// A hold on a live run stays while the run lives and gives way to queued the
// poll after it ends, even though no slot is free.
func TestNoteQueued_LastRunActiveHoldRefreshedWhenRunEnds(t *testing.T) {
	c := lastRunHoldDispatcher(t)
	writeRunDoc(t, c.storeDir, "run-1", store.RunStatusRunning, nil)

	iss := tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "in_progress", Bot: "wf"}
	if !c.lastRunHoldBeforeClaim(iss) {
		t.Fatal("precondition: a live last run must hold the card")
	}
	if sk := c.state.dispatchSkips["i1"]; sk.Kind != DispatchSkipLastRunActive {
		t.Fatalf("precondition: want last_run_active, got %+v", sk)
	}

	cmdCandidates{issues: []tracker.Issue{iss}}.apply(c, context.Background())
	if sk := c.state.dispatchSkips["i1"]; sk.Kind != DispatchSkipLastRunActive {
		t.Fatalf("run still running: the hold must not be lost, got %+v", sk)
	}

	setRunStatus(t, c.storeDir, "run-1", store.RunStatusFinished)
	cmdCandidates{issues: []tracker.Issue{iss}}.apply(c, context.Background())
	sk := c.state.dispatchSkips["i1"]
	if sk.Kind != DispatchSkipQueued {
		t.Fatalf("run finished: want queued, got %+v", sk)
	}
}

func TestRecordLastRunHold_Kinds(t *testing.T) {
	cases := []struct {
		name  string
		setup func(c *Dispatcher)
		iss   tracker.Issue
		want  string
	}{
		{
			name:  "running run",
			setup: func(c *Dispatcher) { writeRunDoc(t, c.storeDir, "run-1", store.RunStatusRunning, nil) },
			iss:   tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "in_progress", Bot: "wf"},
			want:  DispatchSkipLastRunActive,
		},
		{
			name:  "finished run returned by the dispatcher to the same bot",
			setup: func(c *Dispatcher) { writeRunDoc(t, c.storeDir, "run-1", store.RunStatusFinished, nil) },
			iss:   tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "review", Bot: "wf", StateReason: tracker.ReasonRunFinished},
			want:  DispatchSkipRunFinished,
		},
		{
			name:  "cancelled run",
			setup: func(c *Dispatcher) { writeRunDoc(t, c.storeDir, "run-1", store.RunStatusCancelled, nil) },
			iss:   tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "ready", Bot: "wf"},
			want:  DispatchSkipLastRunHold,
		},
		{
			name:  "run store cannot be opened",
			setup: func(c *Dispatcher) { c.storeDir = "" },
			iss:   tracker.Issue{ID: "i1", Identifier: "T-1", WorkflowState: "ready", Bot: "wf"},
			want:  DispatchSkipLastRunHold,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := lastRunHoldDispatcher(t)
			tc.setup(c)
			if !c.lastRunHoldBeforeClaim(tc.iss) {
				t.Fatal("the card must be held")
			}
			if sk := c.state.dispatchSkips["i1"]; sk.Kind != tc.want {
				t.Fatalf("kind = %q, want %q (reason %q)", sk.Kind, tc.want, sk.Reason)
			}
		})
	}
}
