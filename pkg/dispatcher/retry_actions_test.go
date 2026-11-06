package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// These tests drive the retry-queue row actions the way the dispatcher's
// other command tests do: the actor is not running, the command is applied by
// hand, and the API call that waits on its answer runs on a goroutine.

func newQueuedRetry(t *testing.T, id string, attempt int, due time.Duration) (*Dispatcher, *fakeTracker, *retryEntry) {
	t.Helper()
	ft := newFakeTracker()
	ft.issues[id] = &tracker.Issue{ID: id, Identifier: "T-" + id, Title: "queued", WorkflowState: "ready"}
	c := routerTestDispatcher(t, ft)
	e := &retryEntry{
		IssueID: id, Identifier: "T-" + id, Attempt: attempt,
		DueAt: time.Now().Add(due), PrevRunID: "run-prev",
		Timer: time.AfterFunc(time.Hour, func() {}),
	}
	c.state.retries[id] = e
	t.Cleanup(func() {
		if e.Timer != nil {
			e.Timer.Stop()
		}
	})
	return c, ft, e
}

func TestCmdRetryNow_MakesTheRetryDueAndKeepsItsAttempt(t *testing.T) {
	c, _, e := newQueuedRetry(t, "i1", 3, time.Hour)
	reply := make(chan retryNowReply, 1)
	cmdRetryNow{issueID: "i1", reply: reply}.apply(c, context.Background())

	got := <-reply
	if !got.found {
		t.Fatal("the queued retry was not found")
	}
	if !e.Fired {
		t.Error("a forced retry must be eligible at once (Fired)")
	}
	if e.Attempt != 3 || e.PrevRunID != "run-prev" {
		t.Errorf("attempt/resume pointer changed: attempt=%d prev=%q", e.Attempt, e.PrevRunID)
	}
	if time.Until(e.DueAt) > time.Second {
		t.Errorf("DueAt = %v, want now", e.DueAt)
	}
}

func TestCmdRetryNow_UnknownIssueIsNotFound(t *testing.T) {
	c, _, _ := newQueuedRetry(t, "i1", 1, time.Hour)
	reply := make(chan retryNowReply, 1)
	cmdRetryNow{issueID: "nope", reply: reply}.apply(c, context.Background())
	if got := <-reply; got.found {
		t.Fatal("an unknown issue was reported as found")
	}
}

// While a removal is being written the entry is held: neither a timer that
// fires meanwhile nor a forced retry can free the card.
func TestHoldRetry_BlocksTheTimerAndRetryNowUntilReleased(t *testing.T) {
	c, _, e := newQueuedRetry(t, "i1", 2, -time.Minute)
	ctx := context.Background()

	hold := make(chan holdRetryReply, 1)
	cmdHoldRetry{issueID: "i1", reply: hold}.apply(c, ctx)
	h := <-hold
	if !h.found || h.attempt != 2 || h.prevRunID != "run-prev" || h.identifier != "T-i1" {
		t.Fatalf("hold reply = %+v", h)
	}
	if h.failedState == "" {
		t.Fatal("the default config must carry a failed state (blocked)")
	}
	if !e.Removing || e.Fired {
		t.Fatalf("held entry: removing=%v fired=%v", e.Removing, e.Fired)
	}

	cmdRetryDue{issueID: "i1"}.apply(c, ctx)
	if e.Fired {
		t.Error("a timer firing during the removal freed the card")
	}
	now := make(chan retryNowReply, 1)
	cmdRetryNow{issueID: "i1", reply: now}.apply(c, ctx)
	if (<-now).found || e.Fired {
		t.Error("a forced retry went through a held entry")
	}
	again := make(chan holdRetryReply, 1)
	cmdHoldRetry{issueID: "i1", reply: again}.apply(c, ctx)
	if (<-again).found {
		t.Error("a second removal of the same entry must be refused")
	}

	cmdReleaseRetryHold{issueID: "i1"}.apply(c, ctx)
	if e.Removing {
		t.Error("release did not lift the hold")
	}
	if !e.Fired {
		t.Error("an entry whose time had passed must be due again after the release")
	}
}

// "Remove from queue": the card is filed under the failed state and the entry
// is gone, so the next tick does not relaunch it from attempt 0.
func TestGiveUpRetry_FilesTheCardAndDropsTheEntry(t *testing.T) {
	c, ft, _ := newQueuedRetry(t, "i1", 2, time.Hour)
	ctx := context.Background()

	type result struct {
		state string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		s, err := c.GiveUpRetry(ctx, "i1")
		done <- result{s, err}
	}()
	applyNextCmd(t, c, ctx) // cmdHoldRetry
	applyNextCmd(t, c, ctx) // cmdDropRetry, posted once the card is filed

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("GiveUpRetry: %v", r.err)
		}
		want := c.cfg.Load().Agent.FailedState
		if r.state != want {
			t.Fatalf("filed under %q, want %q", r.state, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GiveUpRetry did not return")
	}
	ft.mu.Lock()
	state := ft.issues["i1"].WorkflowState
	ft.mu.Unlock()
	if state != c.cfg.Load().Agent.FailedState {
		t.Errorf("card state = %q, want the failed state", state)
	}
	if _, ok := c.state.retries["i1"]; ok {
		t.Error("the retry entry survived its removal")
	}
}

// With no failed state to file the card under, the entry is NOT dropped: that
// would turn the removal into a fresh paid launch.
func TestGiveUpRetry_WithoutAFailedStateLeavesTheEntry(t *testing.T) {
	c, _, e := newQueuedRetry(t, "i1", 2, time.Hour)
	cfg := c.cfg.Load()
	cfg.Agent.FailedState = ""
	c.cfg.Store(cfg)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := c.GiveUpRetry(ctx, "i1")
		done <- err
	}()
	applyNextCmd(t, c, ctx) // cmdHoldRetry
	applyNextCmd(t, c, ctx) // cmdReleaseRetryHold

	select {
	case err := <-done:
		if !errors.Is(err, ErrRetryNoFailedState) {
			t.Fatalf("err = %v, want ErrRetryNoFailedState", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GiveUpRetry did not return")
	}
	if cur, ok := c.state.retries["i1"]; !ok || cur != e || cur.Removing {
		t.Fatalf("the entry must stay queued and unheld, got %+v ok=%v", cur, ok)
	}
}

func TestServeRetryDrop_UnknownIssueIs404(t *testing.T) {
	c, _, _ := newQueuedRetry(t, "i1", 1, time.Hour)
	req := httptest.NewRequest(http.MethodDelete, "/retries/nope", nil)
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		c.serveRetryDrop(rec, req)
		close(done)
	}()
	applyNextCmd(t, c, context.Background()) // cmdHoldRetry → not found
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not return")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
