package dispatcher

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/store"
)

// sourceChangedFixture runs the failed_resumable card of newLastRunFixture
// through finishRun(ErrWorkflowSourceChanged), as the dispatcher does after a
// doomed resume of a run whose bot was edited, and waits for the finish worker.
func sourceChangedFixture(t *testing.T) *lastRunFixture {
	t.Helper()
	fx := newLastRunFixture(t, store.RunStatusFailedResumable, native.StateInProgress)
	c := fx.disp
	ctx := context.Background()
	if err := c.tracker.Claim(ctx, fx.issue.ID, c.hostMarker); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	c.claims.Record(claimEntry{
		IssueID: fx.issue.ID, Identifier: fx.issue.ID, Marker: c.hostMarker, ClaimedAt: time.Now().UTC(),
	})
	c.state.running[fx.issue.ID] = &runningEntry{
		IssueID: fx.issue.ID, Identifier: fx.issue.ID, RunID: fx.runID,
		WorkflowState: native.StateInProgress, WorkspacePath: filepath.Join(c.storeDir, "ws", "x"),
		StartedAt: time.Now(), TransitionedFromState: native.StateReady,
	}
	c.state.slotsByState[native.StateInProgress] = 1
	c.finishRun(ctx, fx.issue.ID, runtime.ErrWorkflowSourceChanged)
	c.workersWG.Wait()
	return fx
}

func (fx *lastRunFixture) setRunStatus(t *testing.T, status store.RunStatus) {
	t.Helper()
	run, err := fx.runStore.LoadRun(context.Background(), fx.runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	run.Status = status
	if err := fx.runStore.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

func (fx *lastRunFixture) card(t *testing.T) *native.Issue {
	t.Helper()
	got, err := fx.board.Get(fx.issue.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got
}

func (fx *lastRunFixture) dispatchCard(state string) {
	fx.disp.dispatch(context.Background(), tracker.Issue{
		ID: fx.issue.ID, Identifier: fx.issue.ID, Title: fx.issue.Title, WorkflowState: state,
	})
}

func TestFinishRun_SourceChangedHoldsCardInReady(t *testing.T) {
	fx := sourceChangedFixture(t)
	c := fx.disp
	got := fx.card(t)
	if got.State != native.StateReady {
		t.Errorf("card state = %q, want %q (a source change is not a pause: no awaiting_input)", got.State, native.StateReady)
	}
	if got.Claim != "" {
		t.Errorf("claim = %q, want released", got.Claim)
	}
	if got.AwaitingInput {
		t.Error("AwaitingInput must stay false")
	}
	if got.LastRunID != fx.runID {
		t.Errorf("last_run = %q, want %q kept", got.LastRunID, fx.runID)
	}
	if len(c.state.retries) != 0 {
		t.Errorf("retries = %d, want none", len(c.state.retries))
	}
	h, ok := c.state.sourceHolds[fx.issue.ID]
	if !ok || h.runID != fx.runID {
		t.Fatalf("source hold = %+v, want one on run %q", h, fx.runID)
	}
	skip := c.state.dispatchSkips[fx.issue.ID]
	if skip.Kind != DispatchSkipSourceChanged {
		t.Errorf("skip kind = %q, want %q", skip.Kind, DispatchSkipSourceChanged)
	}
	if !strings.Contains(skip.Reason, "resume --force") || !strings.Contains(skip.Reason, "clear-last-run") {
		t.Errorf("skip reason must name both remedies, got %q", skip.Reason)
	}
}

func TestDispatch_HoldsSourceChangedWithoutClaim(t *testing.T) {
	fx := sourceChangedFixture(t)
	for i := 0; i < 2; i++ {
		fx.dispatchCard(native.StateReady)
		fx.disp.workersWG.Wait()
	}
	if fx.launched != 0 {
		t.Fatalf("launched %d run(s), want 0", fx.launched)
	}
	if len(fx.disp.state.running) != 0 {
		t.Errorf("running = %d, want 0", len(fx.disp.state.running))
	}
	if got := fx.card(t); got.Claim != "" || got.State != native.StateReady {
		t.Errorf("card claim=%q state=%q, want untouched (no claim churn)", got.Claim, got.State)
	}
	if fx.disp.state.dispatchSkips[fx.issue.ID].Kind != DispatchSkipSourceChanged {
		t.Error("the source_changed skip must stay visible")
	}
}

func TestReconcileSourceHolds_ForceResumeMovesCardToInProgress(t *testing.T) {
	fx := sourceChangedFixture(t)
	c := fx.disp
	ctx := context.Background()

	fx.setRunStatus(t, store.RunStatusRunning)
	c.reconcileSourceHolds(ctx)
	if got := fx.card(t); got.State != native.StateInProgress || got.AwaitingInput {
		t.Errorf("card state=%q awaiting=%v, want in_progress/false after a force-resume", got.State, got.AwaitingInput)
	}
	if _, ok := c.state.dispatchSkips[fx.issue.ID]; ok {
		t.Error("the badge must be gone once the run is going")
	}
	if h := c.state.sourceHolds[fx.issue.ID]; h == nil || !h.adopted {
		t.Fatalf("hold = %+v, want adopted", h)
	}
	// The adopted card is a candidate in in_progress: it must not be refused
	// with a fresh skip nor claimed.
	fx.dispatchCard(native.StateInProgress)
	if fx.launched != 0 || fx.card(t).Claim != "" {
		t.Error("an adopted card must not be dispatched or claimed")
	}

	fx.setRunStatus(t, store.RunStatusFinished)
	c.reconcileSourceHolds(ctx)
	if got := fx.card(t); got.State != "review" {
		t.Errorf("card state = %q, want review once the run finished", got.State)
	}
	if _, ok := c.state.sourceHolds[fx.issue.ID]; ok {
		t.Error("the hold must be dropped at the terminal status")
	}
}

func TestReconcileSourceHolds_ResetDropsHold(t *testing.T) {
	fx := sourceChangedFixture(t)
	if err := fx.board.SetLastRun(fx.issue.ID, "", ""); err != nil {
		t.Fatalf("SetLastRun: %v", err)
	}
	fx.dispatchCard(native.StateReady)
	fx.disp.workersWG.Wait()
	if _, ok := fx.disp.state.sourceHolds[fx.issue.ID]; ok {
		t.Error("a cleared last_run must drop the hold")
	}
	if fx.launched != 1 {
		t.Errorf("launched %d run(s), want 1 fresh run after the reset", fx.launched)
	}
}

func TestReconcileSourceHolds_ForcedResumeFailsAgain(t *testing.T) {
	fx := sourceChangedFixture(t)
	c := fx.disp
	ctx := context.Background()
	fx.setRunStatus(t, store.RunStatusRunning)
	c.reconcileSourceHolds(ctx)

	fx.setRunStatus(t, store.RunStatusFailedResumable)
	c.reconcileSourceHolds(ctx)
	if got := fx.card(t); got.State != native.StateReady {
		t.Errorf("card state = %q, want ready again", got.State)
	}
	fx.dispatchCard(native.StateReady)
	if fx.disp.state.dispatchSkips[fx.issue.ID].Kind != DispatchSkipSourceChanged {
		t.Error("the badge must come back after the forced resume failed")
	}
	if fx.launched != 0 {
		t.Errorf("launched %d run(s), want 0", fx.launched)
	}
}
