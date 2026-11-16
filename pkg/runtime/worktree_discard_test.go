package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/arketov/iterion/internal/gittest"
)

// discardRepo is a repo whose main branch holds the base commit and whose
// storage branch `branch` carries one extra commit — the shape a finalized
// worktree run leaves behind. Returns the repo, the branch tip, and leaves
// main checked out.
func discardRepo(t *testing.T, branch string) (repo, tip string) {
	t.Helper()
	repo, _ = initBareishRepo(t)
	gittest.Run(t, repo, "checkout", "-q", "-b", branch)
	tip = addCommit(t, repo, "run.txt", "work\n", "run work")
	gittest.Run(t, repo, "checkout", "-q", "-")
	return repo, tip
}

func branchAlive(t *testing.T, repo, branch string) bool {
	t.Helper()
	_, err := gittest.Try(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func TestDiscardRunBranch_UnmergedNeedsForce(t *testing.T) {
	const branch = "iterion/run/x"
	repo, tip := discardRepo(t, branch)

	res, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: branch, ExpectSHA: tip}, nil)
	if !errors.Is(err, ErrBranchUnmerged) {
		t.Fatalf("err = %v, want ErrBranchUnmerged", err)
	}
	if res.Unmerged != 1 {
		t.Errorf("Unmerged = %d, want 1", res.Unmerged)
	}
	if !branchAlive(t, repo, branch) {
		t.Fatal("an unmerged branch must survive a non-forced discard")
	}

	res, err = DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: branch, ExpectSHA: tip, Force: true}, nil)
	if err != nil {
		t.Fatalf("forced discard: %v", err)
	}
	if !res.Deleted || branchAlive(t, repo, branch) {
		t.Errorf("forced discard left the branch (Deleted=%v)", res.Deleted)
	}
}

func TestDiscardRunBranch_MergedBranchIsDeletedWithoutForce(t *testing.T) {
	const branch = "iterion/run/x"
	repo, tip := discardRepo(t, branch)
	gittest.Run(t, repo, "merge", "-q", "--ff-only", branch)

	res, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: branch, ExpectSHA: tip}, nil)
	if err != nil {
		t.Fatalf("discard of an ancestor branch: %v", err)
	}
	if !res.Deleted || branchAlive(t, repo, branch) {
		t.Errorf("merged branch still present (Deleted=%v)", res.Deleted)
	}
}

func TestDiscardRunBranch_CallerVouchedMergeSkipsTheGuard(t *testing.T) {
	// A squash merge never makes the branch tip an ancestor of the target;
	// the caller knows it landed (merge_status=merged) and says so.
	const branch = "iterion/run/x"
	repo, tip := discardRepo(t, branch)

	if _, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: branch, ExpectSHA: tip, Merged: true}, nil); err != nil {
		t.Fatalf("discard of a squash-merged branch: %v", err)
	}
	if branchAlive(t, repo, branch) {
		t.Fatal("branch survived")
	}
}

func TestDiscardRunBranch_RefusesAMovedTip(t *testing.T) {
	const branch = "iterion/run/x"
	repo, tip := discardRepo(t, branch)
	gittest.Run(t, repo, "checkout", "-q", branch)
	addCommit(t, repo, "more.txt", "more\n", "later work")
	gittest.Run(t, repo, "checkout", "-q", "-")

	_, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: branch, ExpectSHA: tip, Force: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("err = %v, want a tip-moved refusal", err)
	}
	if !branchAlive(t, repo, branch) {
		t.Fatal("a branch that moved past final_commit must survive")
	}
}

func TestDiscardRunBranch_RefusesAForeignBranch(t *testing.T) {
	repo, tip := initBareishRepo(t)

	_, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: "main", ExpectSHA: tip, Force: true, Merged: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "iterion/run/") {
		t.Fatalf("err = %v, want a prefix refusal", err)
	}
	if !branchAlive(t, repo, "main") {
		t.Fatal("main was deleted")
	}
}

func TestDiscardRunBranch_MissingBranchIsNotAnError(t *testing.T) {
	repo, tip := initBareishRepo(t)

	res, err := DiscardRunBranch(DiscardBranchRequest{RepoRoot: repo, Branch: "iterion/run/gone", ExpectSHA: tip}, nil)
	if err != nil {
		t.Fatalf("missing branch: %v", err)
	}
	if res.Deleted {
		t.Error("Deleted = true for a branch that never existed")
	}
}
