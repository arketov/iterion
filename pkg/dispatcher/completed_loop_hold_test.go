package dispatcher

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

// finishedCardReturnedByDispatcher builds the live loop's state: a card with
// a finished last run that the finish worker's auto-transition has just
// returned to `completed` (an eligible column of the board).
func finishedCardReturnedByDispatcher(t *testing.T, c *Dispatcher, board *native.Store, completed string) *native.Issue {
	t.Helper()
	writeRunDoc(t, c.storeDir, "run-finished-1", store.RunStatusFinished, nil)
	iss, err := board.Create(native.Issue{Title: "review card", State: native.StateReady})
	if err != nil {
		t.Fatal(err)
	}
	if err := board.SetLastRun(iss.ID, "run-finished-1", ""); err != nil {
		t.Fatal(err)
	}
	tok, err := board.Claim(iss.ID, c.hostMarker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := board.SetStateOwned(iss.ID, native.StateInProgress, tok); err != nil {
		t.Fatal(err)
	}
	sess := StartClaimSession(c.leaser, iss.ID, tok, func(string, ...any) {}, nil)
	defer sess.Stop()
	c.maybeTransitionToCompleted(context.Background(), iss.ID, "i1", native.StateInProgress, completed, sess)
	if err := board.Release(iss.ID, c.hostMarker); err != nil {
		t.Fatal(err)
	}
	got, err := board.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != completed {
		t.Fatalf("precondition: card in %q, want %q", got.State, completed)
	}
	return got
}

func asCandidate(iss *native.Issue) tracker.Issue {
	return tracker.Issue{ID: iss.ID, Identifier: "i1", WorkflowState: iss.State, StateReason: iss.StateReason}
}

func TestLastRunHold_DispatcherReturnedCardIsHeld(t *testing.T) {
	t.Run("returned by the dispatcher is held", func(t *testing.T) {
		c, board, _ := newReaperHarness(t)
		c.state = newState()
		iss := finishedCardReturnedByDispatcher(t, c, board, native.StateReady)
		if !c.lastRunHoldBeforeClaim(asCandidate(iss)) {
			t.Fatal("a card the dispatcher returned to an eligible column after a clean run was released for re-dispatch — endless loop")
		}
		skip, ok := c.state.dispatchSkips[iss.ID]
		if !ok || !strings.Contains(skip.Reason, "returned the card") {
			t.Fatalf("hold must surface as a dispatch skip naming the cause, got %+v (visible=%v)", skip, ok)
		}
	})
	t.Run("operator drag back re-queues", func(t *testing.T) {
		c, board, _ := newReaperHarness(t)
		c.state = newState()
		iss := finishedCardReturnedByDispatcher(t, c, board, native.StateReady)
		if _, err := board.SetState(iss.ID, native.StateBacklog); err != nil {
			t.Fatal(err)
		}
		got, err := board.SetState(iss.ID, native.StateReady)
		if err != nil {
			t.Fatal(err)
		}
		if c.lastRunHoldBeforeClaim(asCandidate(got)) {
			t.Fatal("an operator's drag out of the column and back must clear the hold (the documented re-queue gesture)")
		}
	})
	t.Run("no stamp keeps the old behaviour", func(t *testing.T) {
		c, board, _ := newReaperHarness(t)
		c.state = newState()
		iss := finishedCardReturnedByDispatcher(t, c, board, native.StateReady)
		cand := asCandidate(iss)
		cand.StateReason = ""
		if c.lastRunHoldBeforeClaim(cand) {
			t.Fatal("a finished last_run on a card with no run_finished stamp must stay free for a fresh run")
		}
	})
}

func TestMaybeTransitionToCompleted_StampsRunFinishedReason(t *testing.T) {
	c, board, _ := newReaperHarness(t)
	iss := finishedCardReturnedByDispatcher(t, c, board, native.StateReview)
	if iss.StateReason != tracker.ReasonRunFinished {
		t.Fatalf("StateReason = %q, want %q", iss.StateReason, tracker.ReasonRunFinished)
	}
	if iss.StateByMachine() {
		t.Fatal("run_finished is descriptive, not a machine reason")
	}
}

// TestEligibleCompletedState_NotRedispatchedAfterCleanFinish replays the
// live loop: review is an eligible column and completed_state. After a clean
// finish the poller's own candidate list still offers the card, and the
// pre-claim hold must refuse it on every following poll.
func TestEligibleCompletedState_NotRedispatchedAfterCleanFinish(t *testing.T) {
	c, board, _ := newReaperHarness(t)
	c.state = newState()
	b, err := board.Board()
	if err != nil {
		t.Fatal(err)
	}
	for i := range b.States {
		if b.States[i].Name == native.StateReview {
			b.States[i].Eligible = true
		}
	}
	if err := board.SetBoard(b); err != nil {
		t.Fatal(err)
	}
	iss := finishedCardReturnedByDispatcher(t, c, board, native.StateReview)

	for poll := 1; poll <= 3; poll++ {
		cands, err := c.tracker.ListCandidates(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var cand *tracker.Issue
		for i := range cands {
			if cands[i].ID == iss.ID {
				cand = &cands[i]
			}
		}
		if cand == nil {
			t.Fatalf("poll %d: precondition: the eligible column must offer the card", poll)
		}
		if !c.lastRunHoldBeforeClaim(*cand) {
			t.Fatalf("poll %d: the card would be re-dispatched after a clean finish", poll)
		}
	}
}

func TestWarnCompletedStateIsLaunchColumn(t *testing.T) {
	cases := []struct {
		name      string
		completed string
		want      bool
	}{
		{"eligible column warns", native.StateReady, true},
		{"non-eligible column is quiet", native.StateReview, false},
		{"none is quiet", "", false},
		{"running state is quiet", native.StateInProgress, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newReaperHarness(t)
			var buf bytes.Buffer
			c.logger = iterlog.New(iterlog.LevelWarn, &buf)
			c.warnCompletedStateIsLaunchColumn(&Config{Agent: AgentConfig{
				RunningState:   native.StateInProgress,
				CompletedState: tc.completed,
			}})
			if got := strings.Contains(buf.String(), "completed_state"); got != tc.want {
				t.Fatalf("warned=%v, want %v (log: %q)", got, tc.want, buf.String())
			}
		})
	}
}
