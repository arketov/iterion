package dispatcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// TransitionMergedIssue covers the studio's "auto-move ticket on merge"
// hook. The cases below pin the four early-exit paths (nil dispatcher,
// empty issue id, empty MergedState, "none" sentinel) AND the happy
// path that drives the tracker. Operators on a board with no MergedState
// configured rely on the silent no-op — without this guard, every
// studio-driven merge would 4xx-noise the logs.

func TestManager_TransitionMergedIssue_IdleFallsBackToNativeStore(t *testing.T) {
	// Regression: a studio-driven merge that lands while the polling
	// actor is down (e.g. a watchexec rebuild window) must still close
	// the ticket. The actor pointer is nil, so the transition falls
	// back to the native store directly. Before this fix the call
	// silently no-op'd and the ticket was stranded in review.
	dir := newTestStoreDir(t)
	ns := newTestNativeStore(t, dir)
	iss, err := ns.Create(native.Issue{Title: "demo", State: native.StateReview})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: ns,
		Logger:      newTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// No Start → cur is nil. No persisted config → DefaultMergedState.
	if err := m.TransitionMergedIssue(context.Background(), iss.ID); err != nil {
		t.Fatalf("idle fallback: TransitionMergedIssue err = %v", err)
	}
	got, err := ns.Get(iss.ID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if got.State != native.StateDone {
		t.Errorf("idle fallback: state = %q, want %q", got.State, native.StateDone)
	}
}

func TestManager_TransitionMergedIssue_IdleHonorsNoneOptOut(t *testing.T) {
	// When the operator disabled the transition (merged_state: none →
	// normalized to ""), the idle fallback must NOT resurrect it by
	// defaulting back to "done".
	dir := seedManagerFixtureWithMergedState(t, "none")
	ns := newTestNativeStore(t, dir)
	iss, err := ns.Create(native.Issue{Title: "demo", State: native.StateReview})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: ns,
		Logger:      newTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Idle (no Start); persisted config opted out.
	if err := m.TransitionMergedIssue(context.Background(), iss.ID); err != nil {
		t.Fatalf("idle opt-out: TransitionMergedIssue err = %v", err)
	}
	got, err := ns.Get(iss.ID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if got.State != native.StateReview {
		t.Errorf("idle opt-out: state = %q, want unchanged %q", got.State, native.StateReview)
	}
}

func TestManager_TransitionMergedIssue_EmptyIssue(t *testing.T) {
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := seedManagerFixture(t)
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Stop()
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No issue id → silent no-op even when MergedState is configured.
	if err := m.TransitionMergedIssue(context.Background(), ""); err != nil {
		t.Errorf("empty issue: TransitionMergedIssue err = %v, want nil", err)
	}
}

func TestManager_TransitionMergedIssue_NoneIsNoOp(t *testing.T) {
	// "none" is the explicit opt-out: the operator wants merged issues to
	// stay put. (Empty no longer belongs here — it now defaults to "done"
	// via DefaultMergedState; that firing path is covered by
	// TestManager_TransitionMergedIssue_HappyPath.)
	for _, mergedState := range []string{"none"} {
		t.Run("merged_state="+mergedState, func(t *testing.T) {
			t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
			dir := seedManagerFixtureWithMergedState(t, mergedState)
			ns := newTestNativeStore(t, dir)
			iss, err := ns.Create(native.Issue{Title: "demo"})
			if err != nil {
				t.Fatalf("create issue: %v", err)
			}
			m, err := NewManager(ManagerOptions{
				StoreDir:    dir,
				NativeStore: ns,
				Logger:      newTestLogger(),
			})
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			defer m.Stop()
			if err := m.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			before := iss.State
			if err := m.TransitionMergedIssue(context.Background(), iss.ID); err != nil {
				t.Errorf("merged_state=%q: TransitionMergedIssue err = %v", mergedState, err)
			}
			got, err := ns.Get(iss.ID)
			if err != nil {
				t.Fatalf("reload issue: %v", err)
			}
			if got.State != before {
				t.Errorf("merged_state=%q: state changed %q → %q, expected unchanged", mergedState, before, got.State)
			}
		})
	}
}

func TestManager_TransitionMergedIssue_HappyPath(t *testing.T) {
	// Operator configures merged_state=done; the studio's merge hook
	// fires for a known issue → tracker.UpdateState lands → next board
	// read shows the issue in the target state. End-to-end happy path.
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := seedManagerFixtureWithMergedState(t, native.StateDone)
	ns := newTestNativeStore(t, dir)
	iss, err := ns.Create(native.Issue{Title: "demo", State: native.StateReview})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: ns,
		Logger:      newTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Stop()
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.TransitionMergedIssue(context.Background(), iss.ID); err != nil {
		t.Fatalf("happy path: TransitionMergedIssue err = %v", err)
	}
	got, err := ns.Get(iss.ID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if got.State != native.StateDone {
		t.Errorf("state = %q, want %q", got.State, native.StateDone)
	}
}

// seedManagerFixtureWithMergedState mirrors seedManagerFixture but
// also stamps agent.merged_state into the persisted config so the
// dispatcher Config().Agent.MergedState exposes the value the merge
// hook reads.
func seedManagerFixtureWithMergedState(t *testing.T, mergedState string) string {
	t.Helper()
	dir := newTestStoreDir(t)
	wfPath := filepath.Join(dir, "flow.bot")
	wfBody := `tool noop:
  command: "true"

workflow minimal:
  entry: noop
  noop -> done
`
	if err := os.WriteFile(wfPath, []byte(wfBody), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	cfg := map[string]any{
		"workflow": wfPath,
		"tracker":  map[string]any{"kind": "native"},
		"agent":    map[string]any{"merged_state": mergedState},
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	cfgPath := filepath.Join(dir, "dispatcher", "dispatcher.json")
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}
