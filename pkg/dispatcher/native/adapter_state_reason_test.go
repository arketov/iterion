package native

import (
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// TestToTrackerIssue_CarriesStateReason: the dispatcher decides whether to
// re-take a card it returned itself from Issue.StateReason, so the adapter
// must carry the provenance across the tracker boundary.
func TestToTrackerIssue_CarriesStateReason(t *testing.T) {
	s := newTestStore(t)
	iss, err := s.Create(Issue{Title: "x", State: StateInProgress})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Claim(iss.ID, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.SetStateOwnedFromReason(iss.ID, StateInProgress, StateReview, tok, tracker.ReasonRunFinished); err != nil || !changed {
		t.Fatalf("SetStateOwnedFromReason: changed=%v err=%v", changed, err)
	}
	got, err := s.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ti := toTrackerIssue(got); ti.StateReason != tracker.ReasonRunFinished {
		t.Fatalf("tracker.Issue.StateReason = %q, want %q", ti.StateReason, tracker.ReasonRunFinished)
	}
}
