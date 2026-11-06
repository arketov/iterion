package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotWorktree_KeepsMergeInProgress: a node boundary inside a
// stopped merge (a tool node left a conflict for the next agent node to
// resolve) must not end the merge. The snapshot used to stage into the real
// index and undo it with `git reset --mixed HEAD`, which deletes MERGE_HEAD
// and the unmerged entries: the resolving agent then found conflict markers
// but no merge, and committed a single-parent commit that never merged the
// branch (merge train, 2026-11-06).
func TestSnapshotWorktree_KeepsMergeInProgress(t *testing.T) {
	dir := gateRepo(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	write("seed.txt", "feature\n")
	gitRun(t, dir, "commit", "-q", "-am", "feature side")
	gitRun(t, dir, "checkout", "-q", "main")
	write("seed.txt", "main\n")
	gitRun(t, dir, "commit", "-q", "-am", "main side")

	merge := exec.Command("git", "-C", dir, "-c", "core.hooksPath=/dev/null", "merge", "--no-ff", "--no-edit", "feature")
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("expected a merge conflict, merge succeeded: %s", out)
	}
	gitDir := strings.TrimSpace(gitRun(t, dir, "rev-parse", "--absolute-git-dir"))
	mergeHead := filepath.Join(gitDir, "MERGE_HEAD")
	if _, err := os.Stat(mergeHead); err != nil {
		t.Fatalf("precondition: no MERGE_HEAD after the conflict: %v", err)
	}
	write("untracked.txt", "new\n")

	commit, err := snapshotWorktree(dir, "refs/iterion/runs/r1/turns/n/0/0")
	if err != nil {
		t.Fatalf("snapshotWorktree: %v", err)
	}
	if commit == "" {
		t.Fatal("dirty worktree produced no snapshot")
	}

	if _, err := os.Stat(mergeHead); err != nil {
		t.Fatalf("snapshot ended the merge: MERGE_HEAD is gone (%v)", err)
	}
	if unmerged := strings.TrimSpace(gitRun(t, dir, "ls-files", "-u")); unmerged == "" {
		t.Fatal("snapshot dropped the unmerged index entries")
	}
	if staged := strings.TrimSpace(gitRun(t, dir, "diff", "--cached", "--name-only", "--diff-filter=A")); staged != "" {
		t.Fatalf("snapshot left files staged in the real index: %q", staged)
	}
	// The snapshot itself holds the worktree as it was, untracked file included.
	if got := strings.TrimSpace(gitRun(t, dir, "show", commit+":untracked.txt")); got != "new" {
		t.Fatalf("snapshot tree lacks the untracked file, got %q", got)
	}
	if !strings.Contains(gitRun(t, dir, "show", commit+":seed.txt"), "<<<<<<<") {
		t.Fatal("snapshot tree should hold the conflicted file as it is on disk")
	}
}
