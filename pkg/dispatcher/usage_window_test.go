package dispatcher

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/backend/delegate"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/retrypolicy"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/store"
)

// clearRetryEnv keeps the host's ITERION_RETRY_* out of the policy under test.
func clearRetryEnv(t *testing.T) {
	t.Helper()
	t.Setenv(retrypolicy.EnvUsageWindow, "")
	t.Setenv(retrypolicy.EnvMaxAttempts, "")
	t.Setenv(retrypolicy.EnvMaxWait, "")
	t.Setenv(retrypolicy.EnvJitter, "")
}

func usageWindowErr(resetAt time.Time) error {
	return &runtime.RuntimeError{
		Code: store.FailureUsageLimitBlocked,
		Cause: &delegate.ErrRateLimited{
			Provider: "claude_code",
			Detail:   "You've hit your limit",
			Kind:     delegate.RateLimitKindUsageWindow,
			ResetAt:  resetAt,
		},
	}
}

func TestUsageWindowWake(t *testing.T) {
	clearRetryEnv(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	pol := retrypolicy.FromEnv()
	off := retrypolicy.Policy{UsageWindow: retrypolicy.UsageWindowOff}

	tests := []struct {
		name   string
		err    error
		pol    retrypolicy.Policy
		wantOK bool
		want   time.Time
	}{
		{"typed reset plus a minute", usageWindowErr(now.Add(2 * time.Hour)), pol, true, now.Add(2*time.Hour + time.Minute)},
		{"code without a time falls back to 15m", &runtime.RuntimeError{Code: store.FailureUsageLimitBlocked, Message: "blocked"}, pol, true, now.Add(16 * time.Minute)},
		{"flattened text", errors.New("rate_limited (claude_code): You've hit your limit"), pol, true, now.Add(16 * time.Minute)},
		{"reset in the past floors at a minute", usageWindowErr(now.Add(-time.Hour)), pol, true, now.Add(time.Minute)},
		{"reset beyond the max wait is capped", usageWindowErr(now.Add(30 * 24 * time.Hour)), pol, true, now.Add(pol.MaxWaitDuration())},
		{"other failure code", &runtime.RuntimeError{Code: store.FailureExpressionFailed, Message: "boom"}, pol, false, time.Time{}},
		{"plain throttle", &runtime.RuntimeError{Code: store.FailureRateLimited, Cause: &delegate.ErrRateLimited{Provider: "claude_code", Kind: delegate.RateLimitKindTransient}}, pol, false, time.Time{}},
		{"nil error", nil, pol, false, time.Time{}},
		{"policy off", usageWindowErr(now.Add(2 * time.Hour)), off, false, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := usageWindowWake(tc.err, now, tc.pol)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("wake = %s, want %s", got, tc.want)
			}
		})
	}
}

// parkedDispatcher builds a dispatcher with one running card (fake:u1) whose
// run is about to fail with a usage-window error, at max_attempts 1 — the
// stand configuration where the old default arm sent the card to Blocked.
func parkedDispatcher(t *testing.T, runner Runner) (*Dispatcher, *stateAwareTracker) {
	t.Helper()
	ft := newStateAwareTracker()
	ft.add(tracker.Issue{ID: "fake:u1", Identifier: "fake#u1", Title: "go", WorkflowState: "in_progress"})
	c, wsDir := newStateTestDispatcher(t, runner, ft, time.Hour, "in_progress")
	cfg := c.cfg.Load()
	cfg.Agent.MaxAttempts = 1
	cfg.Agent.FailedState = "blocked"
	c.cfg.Store(cfg)
	c.state.running["fake:u1"] = &runningEntry{
		IssueID: "fake:u1", Identifier: "fake#u1", RunID: "run-u1",
		WorkflowState: "in_progress",
		WorkspacePath: filepath.Join(wsDir, "fake_u1"),
		StartedAt:     time.Now(), TransitionedFromState: "ready",
	}
	return c, ft
}

func TestFinishRun_UsageWindowParksInsteadOfBlocking(t *testing.T) {
	clearRetryEnv(t)
	c, ft := parkedDispatcher(t, &StubRunner{})
	resetAt := time.Now().Add(2 * time.Hour)

	c.finishRun(context.Background(), "fake:u1", usageWindowErr(resetAt))
	c.workersWG.Wait()

	if got := ft.issueState("fake:u1"); got != "ready" {
		t.Fatalf("issue state = %q, want ready — a provider usage window is not a failed attempt", got)
	}
	for _, call := range ft.calls() {
		if call.newState == "blocked" {
			t.Fatalf("card moved to blocked: %+v", ft.calls())
		}
	}
	r, ok := c.state.retries["fake:u1"]
	if !ok {
		t.Fatal("no retry entry — the card is not parked until the window reopens")
	}
	if r.Attempt != 0 {
		t.Fatalf("retry Attempt = %d, want 0 — the wait must not spend max_attempts", r.Attempt)
	}
	if r.Fired {
		t.Fatal("retry already fired")
	}
	wantDue := resetAt.Add(time.Minute)
	if d := r.DueAt.Sub(wantDue); d < -time.Second || d > time.Second {
		t.Fatalf("DueAt = %s, want ≈ %s", r.DueAt, wantDue)
	}
	if !c.state.usageWallUntil.Equal(r.DueAt) {
		t.Fatalf("usageWallUntil = %s, want %s", c.state.usageWallUntil, r.DueAt)
	}
	if r.Timer != nil {
		r.Timer.Stop()
	}
}

func TestUsageWindowWallHoldsNewPickups(t *testing.T) {
	clearRetryEnv(t)
	started := make(chan string, 4)
	runner := &StubRunner{Handler: func(_ context.Context, spec DispatchSpec) error {
		started <- spec.Issue.ID
		return nil
	}}
	c, ft := parkedDispatcher(t, runner)
	ft.add(tracker.Issue{ID: "fake:u2", Identifier: "fake#u2", Title: "next", WorkflowState: "ready"})

	c.finishRun(context.Background(), "fake:u1", usageWindowErr(time.Now().Add(2*time.Hour)))
	c.workersWG.Wait()
	defer func() {
		if r := c.state.retries["fake:u1"]; r != nil && r.Timer != nil {
			r.Timer.Stop()
		}
	}()

	next := tracker.Issue{ID: "fake:u2", Identifier: "fake#u2", Title: "next", WorkflowState: "ready"}
	cmdCandidates{issues: []tracker.Issue{next}}.apply(c, context.Background())
	c.tick(context.Background())
	c.workersWG.Wait()
	select {
	case id := <-started:
		t.Fatalf("%s was dispatched while the provider window is shut", id)
	default:
	}
	if len(c.state.running) != 0 {
		t.Fatalf("running = %d entries, want 0 while the wall stands", len(c.state.running))
	}
	if c.state.discoveryInFlight {
		t.Fatal("tick started a discovery while the wall stands")
	}

	// The window reopened: picking resumes.
	c.state.usageWallUntil = time.Now().Add(-time.Second)
	cmdCandidates{issues: []tracker.Issue{next}}.apply(c, context.Background())
	select {
	case id := <-started:
		if id != "fake:u2" {
			t.Fatalf("dispatched %s, want fake:u2", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no dispatch after the window reopened")
	}
	c.workersWG.Wait()
}

func TestFinishRun_UsageWindowOffFallsBackToLadder(t *testing.T) {
	clearRetryEnv(t)
	t.Setenv(retrypolicy.EnvUsageWindow, retrypolicy.UsageWindowOff)
	c, ft := parkedDispatcher(t, &StubRunner{})

	c.finishRun(context.Background(), "fake:u1", usageWindowErr(time.Now().Add(2*time.Hour)))
	c.workersWG.Wait()

	if got := ft.issueState("fake:u1"); got != "blocked" {
		t.Fatalf("issue state = %q, want blocked — with the usage-window wait off the ordinary attempt ladder applies", got)
	}
	if !c.state.usageWallUntil.IsZero() {
		t.Fatal("usage wall raised although the policy is off")
	}
}
