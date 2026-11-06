package dispatcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// missingShell is a `shell:` that exists nowhere: the hook cannot start, which
// is what makes after_create fail without needing a real shell.
func missingShell(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-shell")
}

// A failed after_create under persist: keep leaves the workspace on disk; the
// next dispatch must recreate it and run after_create again — never hand the
// bot a folder nothing seeded.
func TestRunWorker_FailedAfterCreateLeavesTheWorkspaceUnseeded(t *testing.T) {
	dir := t.TempDir()
	wsRoot := filepath.Join(dir, "ws")
	orderLog := filepath.Join(dir, "order.log")
	hooks := Hooks{AfterCreate: &Hook{Script: "echo seed", Shell: missingShell(t)}}
	runner := runnerAppending(t, orderLog, nil)
	c, ws := newHookLifecycleDispatcher(t, wsRoot, hooks, runner)

	const issueID = "fake:seed-fail"
	wsPath, created, _, err := ws.CreateForDispatch(issueID, "")
	if err != nil || !created {
		t.Fatalf("CreateForDispatch: created=%v err=%v", created, err)
	}
	entry := hookLifecycleEntry(issueID, "run-seed-fail", wsPath)
	entry.WorkspaceGeneration = ""
	entry.CleanupWorkspaceOnSuccess = false
	spec := DispatchSpec{RunID: "run-seed-fail", WorkspacePath: wsPath}

	c.runWorker(context.Background(), entry, true, spec)

	if got := readLifecycleLog(t, orderLog); len(got) != 0 {
		t.Fatalf("the runner started although after_create failed: %v", got)
	}
	cmd := popRunFinished(t, c)
	if cmd.err == nil || !strings.Contains(cmd.err.Error(), "after_create") {
		t.Fatalf("cmdRunFinished.err = %v, want an error naming after_create", cmd.err)
	}
	if !entry.neverStarted.Load() {
		t.Error("a launch that never reached the runner must be marked neverStarted")
	}

	// The retry: the leftover is recreated and reported as created.
	if err := os.WriteFile(filepath.Join(wsPath, "half-seeded.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, created, reseeded, err := ws.CreateForDispatch(issueID, "")
	if err != nil || !created || !reseeded {
		t.Fatalf("retry: created=%v reseeded=%v err=%v, want the workspace recreated for a second after_create", created, reseeded, err)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "half-seeded.txt")); !os.IsNotExist(err) {
		t.Fatalf("the half-seeded content survived the reset (stat err=%v)", err)
	}
}

// A seed that succeeds (here: no after_create configured) is recorded: the
// next dispatch gets the workspace back as it is, work and all.
func TestRunWorker_RecordsTheWorkspaceAsSeeded(t *testing.T) {
	dir := t.TempDir()
	wsRoot := filepath.Join(dir, "ws")
	orderLog := filepath.Join(dir, "order.log")
	runner := runnerAppending(t, orderLog, nil)
	c, ws := newHookLifecycleDispatcher(t, wsRoot, Hooks{}, runner)

	const issueID = "fake:seed-ok"
	wsPath, created, _, err := ws.CreateForDispatch(issueID, "")
	if err != nil || !created {
		t.Fatalf("CreateForDispatch: created=%v err=%v", created, err)
	}
	entry := hookLifecycleEntry(issueID, "run-seed-ok", wsPath)
	entry.WorkspaceGeneration = ""
	entry.CleanupWorkspaceOnSuccess = false
	c.runWorker(context.Background(), entry, true, DispatchSpec{RunID: "run-seed-ok", WorkspacePath: wsPath})
	if cmd := popRunFinished(t, c); cmd.err != nil {
		t.Fatalf("cmdRunFinished.err = %v", cmd.err)
	}

	work := filepath.Join(wsPath, "bot-output.txt")
	if err := os.WriteFile(work, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, created, reseeded, err := ws.CreateForDispatch(issueID, "")
	if err != nil || created || reseeded {
		t.Fatalf("created=%v reseeded=%v err=%v, want the seeded workspace back as it is", created, reseeded, err)
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the bot's output was wiped: %v", err)
	}
}
