package native

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Phantom run refs (no run on disk) are dropped from the history and the
// pointer falls back to the newest surviving run; claimed cards and young
// entries are left alone.
func TestPruneRunRefs(t *testing.T) {
	s := newTestStore(t)
	iss, err := s.Create(Issue{Title: "ghosts", State: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	// real run first, then two phantoms (newest last).
	s.mu.Lock()
	cur, _ := s.readIssueLocked(iss.ID)
	cur.Runs = []RunRef{
		{RunID: "real-1", Workdir: "/w/real", At: old},
		{RunID: "ghost-1", Workdir: "/w/g1", At: old},
		{RunID: "ghost-2", Workdir: "/w/g2", At: old},
		{RunID: "ghost-young", Workdir: "/w/gy", At: time.Now()},
	}
	cur.LastRunID, cur.LastWorkdir = "ghost-2", "/w/g2"
	cur.LastRunStatus = "failed"
	if err := s.writeIssueLocked(cur); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.setIndexLocked(cur.ID, cloneIssue(cur))
	s.mu.Unlock()

	exists := func(id string) bool { return id == "real-1" }
	pruned, err := s.PruneRunRefs(exists, 10*time.Minute)
	if err != nil {
		t.Fatalf("PruneRunRefs: %v", err)
	}
	if len(pruned) != 1 || len(pruned[0].RunIDs) != 2 {
		t.Fatalf("pruned = %+v, want 2 ghosts on one card", pruned)
	}
	got, _ := s.Get(iss.ID)
	if len(got.Runs) != 2 || got.Runs[0].RunID != "real-1" || got.Runs[1].RunID != "ghost-young" {
		t.Fatalf("Runs = %+v, want real-1 + the young entry", got.Runs)
	}
	// The pointer named a pruned run: cleared (next dispatch is fresh, not a
	// resume of an older run) and the dead pointer's outcome is dropped.
	if got.LastRunID != "" || got.LastRunStatus != "" {
		t.Fatalf("pointer = %q status=%q", got.LastRunID, got.LastRunStatus)
	}
}

func TestPruneRunRefsSkipsClaimedCard(t *testing.T) {
	s := newTestStore(t)
	iss, _ := s.Create(Issue{Title: "claimed", State: "ready"})
	s.mu.Lock()
	cur, _ := s.readIssueLocked(iss.ID)
	cur.Claim = "host-1"
	cur.Runs = []RunRef{{RunID: "ghost", At: time.Now().Add(-time.Hour)}}
	cur.LastRunID = "ghost"
	_ = s.writeIssueLocked(cur)
	s.setIndexLocked(cur.ID, cloneIssue(cur))
	s.mu.Unlock()
	pruned, err := s.PruneRunRefs(func(string) bool { return false }, time.Minute)
	if err != nil || len(pruned) != 0 {
		t.Fatalf("claimed card must be skipped: %+v err=%v", pruned, err)
	}
}

// The disk probe runs without the store lock (a slow drive must not stall Get,
// List and the mutators), and each run id is probed once even when the pointer
// repeats a history entry.
func TestPruneRunRefsDoesNotHoldStoreLockWhileChecking(t *testing.T) {
	s := newTestStore(t)
	iss, err := s.Create(Issue{Title: "ghosts", State: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	s.mu.Lock()
	cur, _ := s.readIssueLocked(iss.ID)
	cur.Runs = []RunRef{
		{RunID: "ghost-1", At: old},
		{RunID: "ghost-2", At: old},
		{RunID: "ghost-3", At: old},
	}
	cur.LastRunID = "ghost-3"
	if err := s.writeIssueLocked(cur); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.setIndexLocked(cur.ID, cloneIssue(cur))
	s.mu.Unlock()

	calls := map[string]int{}
	exists := func(id string) bool {
		calls[id]++
		if !s.mu.TryLock() {
			t.Errorf("store lock held while checking disk for %s", id)
			return false
		}
		s.mu.Unlock()
		return false
	}
	pruned, err := s.PruneRunRefs(exists, 10*time.Minute)
	if err != nil {
		t.Fatalf("PruneRunRefs: %v", err)
	}
	if len(pruned) != 1 || len(pruned[0].RunIDs) != 3 {
		t.Fatalf("pruned = %+v, want 3 ghosts on one card", pruned)
	}
	for _, id := range []string{"ghost-1", "ghost-2", "ghost-3"} {
		if calls[id] != 1 {
			t.Errorf("exists(%s) called %d times, want 1", id, calls[id])
		}
	}
}

// Between the disk check and the write the card may change: a claim wins, and a
// run id the disk check never saw is kept.
func TestPruneRunRefsRechecksCardAfterDiskCheck(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Create(Issue{Title: "A", State: "ready"})
	b, _ := s.Create(Issue{Title: "B", State: "ready"})
	old := time.Now().Add(-time.Hour)
	for id, run := range map[string]string{a.ID: "ghost-a", b.ID: "ghost-b"} {
		s.mu.Lock()
		cur, _ := s.readIssueLocked(id)
		cur.Runs = []RunRef{{RunID: run, At: old}}
		_ = s.writeIssueLocked(cur)
		s.setIndexLocked(cur.ID, cloneIssue(cur))
		s.mu.Unlock()
	}

	first := true
	exists := func(string) bool {
		if first {
			first = false
			if !s.mu.TryLock() {
				t.Error("store lock held while checking disk")
				return false
			}
			ca, _ := s.readIssueLocked(a.ID)
			ca.Claim = "host-1"
			_ = s.writeIssueLocked(ca)
			s.setIndexLocked(ca.ID, cloneIssue(ca))
			cb, _ := s.readIssueLocked(b.ID)
			cb.Runs = append(cb.Runs, RunRef{RunID: "added-later", At: old})
			_ = s.writeIssueLocked(cb)
			s.setIndexLocked(cb.ID, cloneIssue(cb))
			s.mu.Unlock()
		}
		return false
	}
	pruned, err := s.PruneRunRefs(exists, 10*time.Minute)
	if err != nil {
		t.Fatalf("PruneRunRefs: %v", err)
	}
	if len(pruned) != 1 || pruned[0].IssueID != b.ID {
		t.Fatalf("pruned = %+v, want only card B", pruned)
	}
	gotA, _ := s.Get(a.ID)
	if len(gotA.Runs) != 1 || gotA.Runs[0].RunID != "ghost-a" {
		t.Fatalf("claimed card A Runs = %+v, want untouched", gotA.Runs)
	}
	gotB, _ := s.Get(b.ID)
	if len(gotB.Runs) != 1 || gotB.Runs[0].RunID != "added-later" {
		t.Fatalf("card B Runs = %+v, want only added-later", gotB.Runs)
	}
}

// A missing issue answers in the same JSON envelope as every other native
// error, not as bare text.
func TestMissingIssueAnswersJSON(t *testing.T) {
	s := newTestStore(t)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux, "/api/v1/native")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/native/issues/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("want JSON {error}, got %q (%v)", w.Body.String(), err)
	}
}
