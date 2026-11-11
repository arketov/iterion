package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/internal/gittest"
	"github.com/arketov/iterion/pkg/store"
)

// seedFinalizedRun seeds a run with a storage branch in a real repository:
// the repo's main holds the base commit, iterion/run/<id> one extra commit.
func seedFinalizedRun(t *testing.T, srv *Server, runID string, status store.RunStatus, merge store.MergeStatus) (repo, branch string) {
	t.Helper()
	repo = t.TempDir()
	gittest.InitRepo(t, repo)
	branch = "iterion/run/" + runID
	gittest.Run(t, repo, "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(repo, "run.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gittest.Run(t, repo, "add", "run.txt")
	gittest.Run(t, repo, "commit", "-q", "-m", "run work")
	tip := gittest.Run(t, repo, "rev-parse", "HEAD")
	gittest.Run(t, repo, "checkout", "-q", "-")

	seedRun(t, srv, runID, "wf", status)
	st, err := store.New(srv.cfg.StoreDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	r, err := st.LoadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	r.RepoRoot = repo
	r.FinalCommit = tip
	r.FinalBranch = branch
	r.MergeStatus = merge
	if err := st.SaveRun(context.Background(), r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	return repo, branch
}

func postDiscard(t *testing.T, base, runID, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(base+"/api/runs/"+runID+"/discard-branch", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func finalBranchInStore(t *testing.T, srv *Server, runID string) string {
	t.Helper()
	st, err := store.New(srv.cfg.StoreDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	r, err := st.LoadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	return r.FinalBranch
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	_, err := gittest.Try(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func TestDiscardBranchEndpoint_SquashMergedRunDropsItsBranch(t *testing.T) {
	srv, hs := newTestServer(t)
	repo, branch := seedFinalizedRun(t, srv, "r-merged", store.RunStatusFinished, store.MergeStatusMerged)

	code, body := postDiscard(t, hs.URL, "r-merged", `{}`)
	if code != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", code, body)
	}
	if branchExists(t, repo, branch) {
		t.Error("the run branch survived the discard")
	}
	if got := finalBranchInStore(t, srv, "r-merged"); got != "" {
		t.Errorf("final_branch = %q, want it cleared", got)
	}
}

func TestDiscardBranchEndpoint_RefusesALiveRun(t *testing.T) {
	srv, hs := newTestServer(t)
	repo, branch := seedFinalizedRun(t, srv, "r-live", store.RunStatusRunning, store.MergeStatusMerged)

	code, body := postDiscard(t, hs.URL, "r-live", `{"force":true}`)
	if code != http.StatusConflict {
		t.Fatalf("status = %d (%s), want 409", code, body)
	}
	if !branchExists(t, repo, branch) {
		t.Error("a live run's branch was deleted")
	}
}

func TestDiscardBranchEndpoint_UnmergedNeedsForce(t *testing.T) {
	srv, hs := newTestServer(t)
	repo, branch := seedFinalizedRun(t, srv, "r-open", store.RunStatusFinished, store.MergeStatusPending)

	code, body := postDiscard(t, hs.URL, "r-open", `{}`)
	if code != http.StatusConflict || !strings.Contains(body, "branch_unmerged") {
		t.Fatalf("status = %d (%s), want 409 branch_unmerged", code, body)
	}
	if !branchExists(t, repo, branch) || finalBranchInStore(t, srv, "r-open") != branch {
		t.Fatal("a refused discard changed something")
	}

	code, body = postDiscard(t, hs.URL, "r-open", `{"force":true}`)
	if code != http.StatusNoContent {
		t.Fatalf("forced status = %d (%s), want 204", code, body)
	}
	if branchExists(t, repo, branch) {
		t.Error("forced discard left the branch")
	}
}

func TestDiscardBranchEndpoint_UnknownRunIs404(t *testing.T) {
	_, hs := newTestServer(t)
	if code, body := postDiscard(t, hs.URL, "nope", `{}`); code != http.StatusNotFound {
		t.Fatalf("status = %d (%s), want 404", code, body)
	}
}
