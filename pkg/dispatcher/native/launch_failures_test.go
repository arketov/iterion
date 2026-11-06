package native

import (
	"testing"
)

func intp(n int) *int { return &n }

// A failed attempt is stored on the card, survives a reopen of the store (the
// dispatcher stop / studio restart case) and is cleared by the next stamped run.
func TestLaunchFailuresPersistAndClear(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := s.Create(Issue{Title: "hooks fail", State: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	before := iss.UpdatedAt
	f := LaunchFailure{Attempt: 1, RunID: "ghost", Hook: "after_create", ExitCode: intp(128), Error: "after_create hook: exit status 128", Stderr: "fatal: not a git repository", LogPath: "/logs/d.log"}
	if err := s.RecordLaunchFailure(iss.ID, f); err != nil {
		t.Fatalf("RecordLaunchFailure: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LaunchFailures) != 1 {
		t.Fatalf("LaunchFailures = %+v", got.LaunchFailures)
	}
	lf := got.LaunchFailures[0]
	if lf.Hook != "after_create" || lf.ExitCode == nil || *lf.ExitCode != 128 || lf.Stderr == "" || lf.LogPath != "/logs/d.log" {
		t.Fatalf("record lost fields: %+v", lf)
	}
	if !got.UpdatedAt.Equal(before) {
		t.Fatalf("a diagnostic must not bump UpdatedAt (dispatch order): %v -> %v", before, got.UpdatedAt)
	}

	// A launch happened: the failed attempts before it no longer describe the card.
	if err := reopened.SetLastRun(iss.ID, "real-run", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.Get(iss.ID)
	if len(got.LaunchFailures) != 0 {
		t.Fatalf("a stamped run must clear launch failures, got %+v", got.LaunchFailures)
	}
}

func TestLaunchFailuresAreCapped(t *testing.T) {
	s := newTestStore(t)
	iss, _ := s.Create(Issue{Title: "capped", State: "ready"})
	for i := 1; i <= MaxLaunchFailures+5; i++ {
		if err := s.RecordLaunchFailure(iss.ID, LaunchFailure{Attempt: i, Error: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Get(iss.ID)
	if len(got.LaunchFailures) != MaxLaunchFailures {
		t.Fatalf("len = %d, want %d", len(got.LaunchFailures), MaxLaunchFailures)
	}
	if got.LaunchFailures[len(got.LaunchFailures)-1].Attempt != MaxLaunchFailures+5 || got.LaunchFailures[0].Attempt != 6 {
		t.Fatalf("oldest must be dropped, newest kept: first=%d last=%d", got.LaunchFailures[0].Attempt, got.LaunchFailures[len(got.LaunchFailures)-1].Attempt)
	}
}

// The Adapter must expose the capability: an assertion on the optional
// interface fails silently when a pass-through is missing.
func TestAdapterRecordsLaunchFailures(t *testing.T) {
	s := newTestStore(t)
	iss, _ := s.Create(Issue{Title: "via adapter", State: "ready"})
	var r LaunchFailureRecorder = NewAdapter(s)
	if err := r.RecordLaunchFailure(iss.ID, LaunchFailure{Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(iss.ID)
	if len(got.LaunchFailures) != 1 {
		t.Fatalf("not recorded through the adapter: %+v", got.LaunchFailures)
	}
}

// Reopen is the operator's "start this card over": the "did not start" history
// of the attempts that led to the park goes with it, as the launch-refusal
// ledger does — otherwise a reopened card shows "did not start" until its
// first launch.
func TestReopenClearsLaunchFailures(t *testing.T) {
	s := newTestStore(t)
	iss, err := s.Create(Issue{Title: "hooks fail", State: StateInProgress})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunchFailure(iss.ID, LaunchFailure{Attempt: 1, Hook: "after_create", Error: "boom"}); err != nil {
		t.Fatalf("RecordLaunchFailure: %v", err)
	}
	if _, err := s.SetState(iss.ID, StateBlocked); err != nil {
		t.Fatalf("SetState(blocked): %v", err)
	}
	if got, _ := s.Get(iss.ID); len(got.LaunchFailures) != 1 {
		t.Fatalf("setup: LaunchFailures = %+v, want the one recorded attempt", got.LaunchFailures)
	}
	out, err := s.Reopen(iss.ID, StateReady)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if len(out.LaunchFailures) != 0 {
		t.Fatalf("Reopen returned LaunchFailures = %+v, want none", out.LaunchFailures)
	}
	if got, _ := s.Get(iss.ID); len(got.LaunchFailures) != 0 {
		t.Fatalf("stored LaunchFailures = %+v, want none after Reopen", got.LaunchFailures)
	}
}
