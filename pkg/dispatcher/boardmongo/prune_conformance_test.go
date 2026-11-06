package boardmongo_test

import (
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/boardmongo"
	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// runPruneRunRefsSuite pins boardmongo.Store.PruneRunRefs, the cloud twin of
// native.Store.PruneRunRefs.
//
// Properties: (1) a run the probe reports absent leaves the card's history, the
// pointer naming it is cleared with its outcome tuple, UpdatedAt is untouched
// and a last_run event with an empty run id is emitted; (2) a run the probe
// reports present is left alone; (3) a claimed card is skipped, claim intact;
// (4) an entry younger than the grace is left alone; (5) a card that moved
// between the read and the write (CAS guard on updatedat) is skipped, not
// clobbered.
//
// Mutations that redden it: drop the `issue.claim` filter → (3); drop the
// `issue.updatedat` guard → (5).
func runPruneRunRefsSuite(t *testing.T, store *boardmongo.Store) {
	t.Helper()
	// A negative grace puts the cutoff in the future: every entry counts as old.
	const oldEnough = -time.Minute

	stamp := func(title, runID string) *native.Issue {
		t.Helper()
		iss, err := store.Create(native.Issue{Title: title, State: native.StateReady})
		if err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
		if err := store.SetLastRun(iss.ID, runID, ""); err != nil {
			t.Fatalf("SetLastRun(%s): %v", runID, err)
		}
		return iss
	}
	ghost := stamp("ghost card", "ghost-a")
	if err := store.SetRunOutcome(ghost.ID, "ghost-a", "failed", "boom", "SOME_CODE", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("SetRunOutcome: %v", err)
	}
	live := stamp("live card", "live-run")
	claimed := stamp("claimed card", "ghost-b")
	if _, err := store.Claim(claimed.ID, "host-prune"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	before, _ := store.Get(ghost.ID)
	claimedBefore, _ := store.Get(claimed.ID)

	liveOnly := func(id string) bool { return id == "live-run" }

	// (4) Young entries are protected by the grace.
	if pruned, err := store.PruneRunRefs(func(string) bool { return false }, 10*time.Minute); err != nil || len(pruned) != 0 {
		t.Fatalf("PruneRunRefs(grace=10m) = %+v, %v; want nothing pruned", pruned, err)
	}
	// (2) Present runs are left alone.
	if pruned, err := store.PruneRunRefs(func(string) bool { return true }, oldEnough); err != nil || len(pruned) != 0 {
		t.Fatalf("PruneRunRefs(all exist) = %+v, %v; want nothing pruned", pruned, err)
	}
	if got, _ := store.Get(ghost.ID); got.LastRunID != "ghost-a" || len(got.Runs) != 1 {
		t.Fatalf("card touched although every run exists: %+v", got)
	}

	// (1) The absent run goes; the live card and the claimed card stay.
	pruned, err := store.PruneRunRefs(liveOnly, oldEnough)
	if err != nil {
		t.Fatalf("PruneRunRefs: %v", err)
	}
	if len(pruned) != 1 || pruned[0].IssueID != ghost.ID || len(pruned[0].RunIDs) != 1 || pruned[0].RunIDs[0] != "ghost-a" {
		t.Fatalf("pruned = %+v; want only ghost-a of the ghost card", pruned)
	}
	got, _ := store.Get(ghost.ID)
	if len(got.Runs) != 0 || got.LastRunID != "" || got.LastRunStatus != "" || got.LastRunError != "" ||
		got.LastRunErrorCode != "" || got.LastRunAt != nil {
		t.Errorf("ghost card still carries the dead run: runs=%+v pointer=%q status=%q", got.Runs, got.LastRunID, got.LastRunStatus)
	}
	if !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want untouched %v (housekeeping is not an edit)", got.UpdatedAt, before.UpdatedAt)
	}
	cleared := false
	if err := store.ScanEvents(func(e *native.Event) bool {
		if e.Type == native.EvtIssueLastRun && e.IssueID == ghost.ID && e.Payload["run_id"] == "" {
			cleared = true
		}
		return true
	}); err != nil {
		t.Fatalf("ScanEvents: %v", err)
	}
	if !cleared {
		t.Errorf("no last_run event with an empty run id for the pruned pointer")
	}
	if got, _ := store.Get(live.ID); got.LastRunID != "live-run" || len(got.Runs) != 1 {
		t.Errorf("live card changed: %+v", got)
	}
	// (3) The claimed card keeps its run and its claim.
	got, _ = store.Get(claimed.ID)
	if got.LastRunID != "ghost-b" || len(got.Runs) != 1 || got.Claim != claimedBefore.Claim || got.ClaimEpoch != claimedBefore.ClaimEpoch {
		t.Errorf("claimed card touched: %+v (before %+v)", got, claimedBefore)
	}

	// (5) A card edited between the read and the write is skipped, not clobbered.
	racy := stamp("racy card", "ghost-d")
	bumped := false
	pruned, err = store.PruneRunRefs(func(id string) bool {
		if id == "ghost-d" && !bumped {
			bumped = true
			time.Sleep(5 * time.Millisecond)
			if err := store.SetLastRun(racy.ID, "other", ""); err != nil {
				t.Errorf("SetLastRun(other): %v", err)
			}
		}
		return id == "live-run"
	}, oldEnough)
	if err != nil {
		t.Fatalf("PruneRunRefs(race): %v", err)
	}
	for _, p := range pruned {
		if p.IssueID == racy.ID {
			t.Errorf("racy card was pruned from a stale snapshot: %+v", p)
		}
	}
	got, _ = store.Get(racy.ID)
	if got.LastRunID != "other" || len(got.Runs) != 2 {
		t.Errorf("concurrent edit lost: pointer=%q runs=%+v", got.LastRunID, got.Runs)
	}
}
