package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A failed hook reports WHICH hook and WITH WHAT exit code, through the
// wrapping the worker applies ("after_create hook: %w").
func TestHookRunFailureIsStructured(t *testing.T) {
	h := &Hook{Script: "echo boom >&2; exit 7"}
	err := h.Run(context.Background(), newTestLogger(), "after_create", t.TempDir(), nil)
	wrapped := fmt.Errorf("after_create hook: %w", err)

	var he *HookError
	if !errors.As(wrapped, &he) {
		t.Fatalf("want *HookError in chain, got %v", err)
	}
	if he.Hook != "after_create" || he.ExitCode != 7 || he.TimedOut {
		t.Fatalf("HookError = %+v, want hook=after_create exit=7", he)
	}
	if he.Stderr != "boom" {
		t.Fatalf("Stderr = %q, want %q", he.Stderr, "boom")
	}
}

// The retry queue row carries the hook and exit code of the failure.
func TestRetryViewCarriesHookFailure(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	c.state.retries["i1"] = &retryEntry{
		IssueID: "i1", Identifier: "T-1", Attempt: 2,
		LastError: "after_create hook: hook after_create: exit status 3", LastHook: "after_create", LastHookExit: 3,
		LastHookStderr: "no such file",
	}
	snap := c.buildSnapshot()
	if len(snap.Retries) != 1 {
		t.Fatalf("retries = %d", len(snap.Retries))
	}
	r := snap.Retries[0]
	if r.Hook != "after_create" || r.HookExitCode == nil || *r.HookExitCode != 3 || r.HookStderr != "no such file" {
		t.Fatalf("RetryView = %+v", r)
	}
	if r.Error != c.state.retries["i1"].LastError {
		t.Fatalf("Error must be the full text, got %q", r.Error)
	}
}
