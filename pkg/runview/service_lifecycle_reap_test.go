package runview

import (
	"context"
	"testing"

	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

// TestSandboxContainerReapable_NeverReapsLiveRun guards the liveness fix in
// the boot-time sandbox-container reaper (reconcileSandboxContainers): it must
// NOT force-remove a container whose owning run process is still alive (run
// lock held) — even when the stored status looks terminal or is unreadable.
//
// Without the liveness probe, a daemon restart (e.g. studio:dev bouncing under
// watchexec while a concurrent CLI `iterion run` is live in the same store dir)
// would reap that run's container out from under its in-flight docker exec
// (claude_code / claw delegate call), surfacing as a baffling "No such
// container" mid-run.
func TestSandboxContainerReapable_NeverReapsLiveRun(t *testing.T) {
	dir := t.TempDir()
	logger := iterlog.Nop()
	svc, err := NewService(dir, WithLogger(logger))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	st := svc.store

	mustRun := func(id string, status store.RunStatus) {
		t.Helper()
		r, err := st.CreateRun(ctx, id, "wf", nil)
		if err != nil {
			t.Fatalf("CreateRun %s: %v", id, err)
		}
		if status != "" && status != store.RunStatusRunning {
			r.Status = status
			if err := st.SaveRun(ctx, r); err != nil {
				t.Fatalf("SaveRun %s: %v", id, err)
			}
		}
	}

	// Empty runID — a managed container with no run owner → reapable.
	if !svc.sandboxContainerReapable(ctx, "") {
		t.Error("empty runID should be reapable (orphan with no owner)")
	}
	// Missing run record (unlocked + LoadRun error) → NOT reapable: the run
	// may live under a different store/project root (a concurrent cross-store
	// CLI run) whose lock this store cannot see. Never reap what isn't ours —
	// reaping killed live cross-project runs with "No such container" + 137.
	if svc.sandboxContainerReapable(ctx, "ghost-run") {
		t.Error("a run absent from this store must NOT be reapable (could be a live cross-store run)")
	}
	// Unlocked + running → NOT reapable (status path keeps active runs).
	mustRun("r-running", store.RunStatusRunning)
	if svc.sandboxContainerReapable(ctx, "r-running") {
		t.Error("running run should NOT be reapable")
	}
	// Unlocked + terminal → reapable (owner gone, work done).
	mustRun("r-done", store.RunStatusFinished)
	if !svc.sandboxContainerReapable(ctx, "r-done") {
		t.Error("finished + unlocked run should be reapable")
	}

	// THE FIX: a live run (lock HELD) must NOT be reapable even though its
	// status is terminal — liveness wins over a stale / mid-write status.
	mustRun("r-live", store.RunStatusFailed)
	lock, err := st.LockRun(ctx, "r-live")
	if err != nil {
		t.Fatalf("LockRun r-live: %v", err)
	}
	defer func() { _ = lock.Unlock() }()
	if svc.sandboxContainerReapable(ctx, "r-live") {
		t.Error("a run with a HELD lock (process alive) must NOT be reapable, even with a terminal status")
	}
}
