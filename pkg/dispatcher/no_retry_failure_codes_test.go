package dispatcher

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/store"
)

func failNodeErr(code string) error {
	return &runtime.RuntimeError{Code: store.FailureCode(code), NodeID: "blocked", Message: "workflow reached fail node"}
}

// agent.no_retry_failure_codes: a listed code goes straight to failed_state;
// an unlisted code or an empty list keeps the ordinary retry ladder.
func TestFinishRun_NoRetryFailureCodes(t *testing.T) {
	cases := []struct {
		name      string
		list      []string
		code      string
		wantState string // board state after the finish worker and the follow-up command
		wantRetry bool   // retry entry still queued
	}{
		{"code in list: no retry", []string{"IMPL_BLOCKED"}, "IMPL_BLOCKED", "blocked", false},
		{"code not in list: retried as before", []string{"IMPL_BLOCKED"}, "OTHER_CODE", "ready", true},
		{"empty list: unchanged", nil, "IMPL_BLOCKED", "ready", true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "fake:n" + string(rune('1'+i))
			ft := newStateAwareTracker()
			ft.add(tracker.Issue{ID: id, Identifier: "fake#" + id, Title: "go", WorkflowState: "in_progress"})
			c, wsDir := newStateTestDispatcher(t, &StubRunner{}, ft, time.Hour, "in_progress")
			cfg := c.cfg.Load()
			cfg.Agent.FailedState = "blocked"
			cfg.Agent.MaxAttempts = 10 // far from exhausted on attempt 0
			cfg.Agent.NoRetryFailureCodes = tc.list
			c.cfg.Store(cfg)

			c.state.running[id] = &runningEntry{
				IssueID: id, Identifier: "fake#" + id, RunID: "run-" + id,
				WorkflowState: "in_progress",
				WorkspacePath: filepath.Join(wsDir, "ws"),
				StartedAt:     time.Now(), TransitionedFromState: "ready",
			}

			c.finishRun(context.Background(), id, failNodeErr(tc.code))
			c.workersWG.Wait()
			if !tc.wantRetry {
				applyNextCmd(t, c, context.Background())
			}

			if got := ft.issueState(id); got != tc.wantState {
				t.Errorf("issue state = %q, want %q", got, tc.wantState)
			}
			if _, ok := c.state.retries[id]; ok != tc.wantRetry {
				t.Errorf("retry queued = %v, want %v", ok, tc.wantRetry)
			}
		})
	}
}

func TestConfigNormalisesNoRetryFailureCodes(t *testing.T) {
	cfg := &Config{}
	cfg.Agent.NoRetryFailureCodes = []string{" IMPL_BLOCKED ", "", "  "}
	cfg.ApplyDefaults()
	if got := cfg.Agent.NoRetryFailureCodes; len(got) != 1 || got[0] != "IMPL_BLOCKED" {
		t.Fatalf("codes = %q, want [IMPL_BLOCKED]", got)
	}
}
