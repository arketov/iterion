package runtime

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/backend/model"
	"github.com/arketov/iterion/pkg/store"
)

const requireWorktreeBot = `tool hello:
  command: "echo hi"

workflow w:
  worktree: auto
  sandbox: none
  entry: hello
  hello -> done
`

// runRequireWorktree runs the bot in ws (the workspace) with the store in a
// separate directory, optionally requiring a worktree.
func runRequireWorktree(t *testing.T, ws string, require bool) (store.RunStatus, error) {
	t.Helper()
	const runID = "require-worktree"
	wf := compileBotText(t, requireWorktreeBot)
	exe := model.NewClawExecutor(nil, wf, model.WithWorkDir(ws))
	t.Cleanup(func() { _ = exe.Close() })
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := []EngineOption{WithWorkDir(ws), WithSandboxOverride("none")}
	if require {
		opts = append(opts, WithRequireWorktree())
	}
	runErr := New(wf, s, exe, opts...).Run(t.Context(), runID, nil)
	run, err := s.LoadRun(t.Context(), runID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	return run.Status, runErr
}

// An unattended board launch must not silently run in place in the operator's
// project folder: with WithRequireWorktree a worktree:auto run on a folder that
// cannot host a worktree fails at setup, before any node (or workspace scan).
// Without the option the documented degrade to in-place is unchanged.
func TestRequireWorktreeRefusesInPlaceDegrade(t *testing.T) {
	t.Run("non-git folder refused", func(t *testing.T) {
		status, err := runRequireWorktree(t, t.TempDir(), true)
		if err == nil || !strings.Contains(err.Error(), "not a git repository") {
			t.Fatalf("Run error = %v, want one naming the missing git repository", err)
		}
		if status != store.RunStatusFailed {
			t.Errorf("status = %q, want failed", status)
		}
	})
	t.Run("non-git folder degrades without the option", func(t *testing.T) {
		status, err := runRequireWorktree(t, t.TempDir(), false)
		if err != nil {
			t.Fatalf("Run: %v — the in-place degrade must stay the default", err)
		}
		if status != store.RunStatusFinished {
			t.Errorf("status = %q, want finished", status)
		}
	})
	t.Run("repository with no commits refused", func(t *testing.T) {
		ws := t.TempDir()
		if out, err := exec.Command("git", "-C", ws, "init").CombinedOutput(); err != nil {
			t.Skipf("git init unavailable: %v: %s", err, out)
		}
		status, err := runRequireWorktree(t, ws, true)
		if err == nil || !strings.Contains(err.Error(), "no commits") {
			t.Fatalf("Run error = %v, want one naming the missing commits", err)
		}
		if status != store.RunStatusFailed {
			t.Errorf("status = %q, want failed", status)
		}
	})
}
