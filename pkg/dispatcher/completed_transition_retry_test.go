package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

// flakyMoveLeaser wraps the real lease backend and fails the first failFirst
// fenced CAS moves with err, then delegates. It also records the card's
// column at the instant of the release, which is what proves "transition
// first, Release last" held across the retries.
type flakyMoveLeaser struct {
	tracker.ClaimLeaser
	board     *native.Store
	failFirst int32 // < 0 = fail for ever
	err       error

	moveCalls    atomic.Int32
	releaseCalls atomic.Int32
	mu           sync.Mutex
	stateAtRel   string
}

func (f *flakyMoveLeaser) UpdateStateOwnedFrom(ctx context.Context, id, from, to string, tok tracker.ClaimToken) (bool, error) {
	n := f.moveCalls.Add(1)
	if f.failFirst < 0 || n <= f.failFirst {
		return false, f.err
	}
	return f.ClaimLeaser.(ownedFromUpdater).UpdateStateOwnedFrom(ctx, id, from, to, tok)
}

func (f *flakyMoveLeaser) ReleaseOwned(ctx context.Context, id string, tok tracker.ClaimToken) error {
	f.releaseCalls.Add(1)
	if iss, err := f.board.Get(id); err == nil {
		f.mu.Lock()
		f.stateAtRel = iss.State
		f.mu.Unlock()
	}
	return f.ClaimLeaser.ReleaseOwned(ctx, id, tok)
}

// newCompletionRetryHarness builds a full Dispatcher (channels, state) over a
// real native board whose lease backend is wrapped by flakyMoveLeaser, and a
// claimed in_progress card with a heartbeat session — the finish worker's
// inputs.
func newCompletionRetryHarness(t *testing.T, failFirst int32) (*Dispatcher, *flakyMoveLeaser, *native.Issue, *claimSession) {
	t.Helper()
	dir := t.TempDir()
	board, err := native.NewStore(filepath.Join(dir, "dispatcher"))
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	t.Cleanup(func() { _ = board.Close() })
	cfg := &Config{
		Name:     "test",
		Workflow: filepath.Join(t.TempDir(), "fake.bot"),
		Tracker:  TrackerConfig{Kind: "native"},
		Polling:  PollingConfig{IntervalMS: 50},
		Agent: AgentConfig{
			MaxConcurrent:  4,
			RunningState:   "in_progress",
			CompletedState: "review",
			FailedState:    "blocked",
		},
		Workspace: WorkspaceConfig{Root: filepath.Join(dir, "ws")},
	}
	cfg.applyDefaults()
	ws, err := NewWorkspaces(cfg.Workspace.Root)
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	adapter := native.NewAdapter(board)
	c, err := New(Options{
		Config: cfg, Tracker: adapter, Runner: &StubRunner{},
		Workspaces: ws, Logger: iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test-host-1", StoreDir: dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	leaser := &flakyMoveLeaser{
		ClaimLeaser: adapter, board: board, failFirst: failFirst,
		err: errors.New("native store: write issue: store: rename temp file: Access is denied"),
	}
	c.leaser = leaser

	iss, err := board.Create(native.Issue{Title: "finished", State: native.StateReady})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tok, err := board.Claim(iss.ID, c.hostMarker)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := board.SetStateOwned(iss.ID, native.StateInProgress, tok); err != nil {
		t.Fatalf("SetStateOwned: %v", err)
	}
	sess := StartClaimSession(c.leaser, iss.ID, tok, func(string, ...any) {}, nil)
	t.Cleanup(sess.Stop)
	return c, leaser, iss, sess
}

func completedPlan(iss *native.Issue, sess *claimSession) finishPlan {
	return finishPlan{
		kind:           finishCompleted,
		issueID:        iss.ID,
		identifier:     iss.ID,
		runningTarget:  native.StateInProgress,
		completedState: native.StateReview,
		runID:          "run-done",
		retryCap:       time.Second,
		session:        sess,
	}
}

// TestFinishWorker_RetriesFailedCompletedMove: a transient failure of the
// completed-state write (the Windows rename blip of the live case) must not
// strand the card in the eligible running column. The worker keeps the claim,
// retries with backoff, surfaces the pending move as a completion_pending
// skip while it fails, clears it once the move lands, and releases LAST.
func TestFinishWorker_RetriesFailedCompletedMove(t *testing.T) {
	c, leaser, iss, sess := newCompletionRetryHarness(t, 2)
	c.completionBackoffFn = func(int, time.Duration) time.Duration { return 5 * time.Millisecond }

	c.runFinishWorker(completedPlan(iss, sess))

	got, err := leaser.board.Get(iss.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != native.StateReview {
		t.Fatalf("card state = %q, want %q: a failed completed-state move was never retried", got.State, native.StateReview)
	}
	if n := leaser.moveCalls.Load(); n != 3 {
		t.Errorf("move attempts = %d, want 3 (two failures, then the write that lands)", n)
	}
	if leaser.releaseCalls.Load() != 1 {
		t.Errorf("release calls = %d, want 1", leaser.releaseCalls.Load())
	}
	leaser.mu.Lock()
	atRelease := leaser.stateAtRel
	leaser.mu.Unlock()
	if atRelease != native.StateReview {
		t.Errorf("the claim was released while the card read %q: Release must come AFTER the move", atRelease)
	}

	// Replay what the worker posted to the actor, in order.
	sawPending, sawDone := false, false
	for {
		select {
		case m := <-c.cmds:
			m.apply(c, context.Background())
			sk, ok := c.state.dispatchSkips[iss.ID]
			switch m.(type) {
			case cmdCompletionPending:
				sawPending = true
				if !ok || sk.Kind != DispatchSkipCompletionPending || !strings.Contains(sk.Reason, "rename temp file") {
					t.Errorf("after cmdCompletionPending the skip = %+v, want kind %q carrying the error", sk, DispatchSkipCompletionPending)
				}
			case cmdCompletionDone:
				sawDone = true
				if ok {
					t.Errorf("cmdCompletionDone left the skip %+v behind", sk)
				}
			}
		default:
			if !sawPending || !sawDone {
				t.Fatalf("pending posted = %v, done posted = %v, want both", sawPending, sawDone)
			}
			return
		}
	}
}

// TestFinishWorker_StopsRetryOnShutdown: the retry loop is not load-bearing for
// correctness (the dispatch-time guard finishes the move after a restart), so
// a stopping dispatcher must not be held up by it — and must still release.
func TestFinishWorker_StopsRetryOnShutdown(t *testing.T) {
	c, leaser, iss, sess := newCompletionRetryHarness(t, -1)
	c.completionBackoffFn = func(int, time.Duration) time.Duration { return time.Hour }

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.runFinishWorker(completedPlan(iss, sess))
	}()
	deadline := time.After(5 * time.Second)
	for leaser.moveCalls.Load() < 1 {
		select {
		case <-deadline:
			t.Fatal("the worker never attempted the move")
		case <-time.After(time.Millisecond):
		}
	}
	c.stopOnce.Do(func() { close(c.stop) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the retry loop ignored the dispatcher shutdown")
	}
	if leaser.releaseCalls.Load() != 1 {
		t.Errorf("release calls = %d, want 1 after the loop gave way to shutdown", leaser.releaseCalls.Load())
	}
}

// setRunFinishedAt rewrites the fixture run's FinishedAt.
func setRunFinishedAt(t *testing.T, fx *lastRunFixture, at time.Time) {
	t.Helper()
	run, err := fx.runStore.LoadRun(context.Background(), fx.runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	run.FinishedAt = &at
	if err := fx.runStore.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
}

// candidateOf is the card as ListCandidates would hand it to dispatch (with the
// column-entry stamp the guard reads).
func candidateOf(t *testing.T, fx *lastRunFixture, state string) tracker.Issue {
	t.Helper()
	got, err := fx.board.Get(fx.issue.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return tracker.Issue{
		ID: got.ID, Identifier: got.ID, Title: got.Title,
		WorkflowState: state, StateAt: got.StateAt,
	}
}

// TestDispatch_UnfinishedTransitionCompletesInsteadOfNewRun is the live case:
// the run finished, the move to completed_state was lost, the card sits in the
// eligible running column and entered it BEFORE the run finished. The next tick
// must finish the move, not buy a second run.
func TestDispatch_UnfinishedTransitionCompletesInsteadOfNewRun(t *testing.T) {
	fx := newLastRunFixture(t, store.RunStatusFinished, native.StateInProgress)
	setRunFinishedAt(t, fx, time.Now().Add(time.Hour)) // after the card entered in_progress

	fx.disp.dispatch(context.Background(), candidateOf(t, fx, native.StateInProgress))
	fx.disp.workersWG.Wait()

	if fx.launched != 0 {
		t.Fatalf("a finished run whose move was lost launched %d new run(s), want 0", fx.launched)
	}
	got, err := fx.board.Get(fx.issue.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != native.StateReview {
		t.Errorf("card state = %q, want %q (the unfinished move is completed)", got.State, native.StateReview)
	}
	if got.Claim != "" {
		t.Errorf("card still claimed by %q after the move", got.Claim)
	}
	if len(fx.disp.state.running) != 0 {
		t.Errorf("completing the move must not take a run slot: %d running", len(fx.disp.state.running))
	}
}

// TestDispatch_RequeueIntoRunningStateAfterFinishStillRuns: the operator who
// drags a finished card into in_progress AFTER the run finished is re-queuing
// it — a fresh run starts (the contract lastRunForbidsFresh documents).
func TestDispatch_RequeueIntoRunningStateAfterFinishStillRuns(t *testing.T) {
	fx := newLastRunFixture(t, store.RunStatusFinished, native.StateInProgress)
	setRunFinishedAt(t, fx, time.Now().Add(-time.Hour)) // card entered the column later

	fx.disp.dispatch(context.Background(), candidateOf(t, fx, native.StateInProgress))
	fx.disp.workersWG.Wait()

	if fx.launched != 1 {
		t.Fatalf("re-queue into in_progress launched %d run(s), want 1 fresh run", fx.launched)
	}
	if fx.lastSpec.RunID == "" || fx.lastSpec.RunID == fx.runID {
		t.Errorf("spec run = %q, want a freshly minted id (not %q)", fx.lastSpec.RunID, fx.runID)
	}
}

// TestDispatch_FinishedLastRunFromReadyStillRuns: the guard is limited to the
// running column — a finished last_run on a ready card is the plain re-queue.
func TestDispatch_FinishedLastRunFromReadyStillRuns(t *testing.T) {
	fx := newLastRunFixture(t, store.RunStatusFinished, native.StateReady)
	setRunFinishedAt(t, fx, time.Now().Add(time.Hour))

	fx.disp.dispatch(context.Background(), candidateOf(t, fx, native.StateReady))
	fx.disp.workersWG.Wait()

	if fx.launched != 1 {
		t.Fatalf("finished last_run on a ready card launched %d run(s), want 1 fresh run", fx.launched)
	}
}
