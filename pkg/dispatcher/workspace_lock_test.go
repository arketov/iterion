package dispatcher

import (
	"testing"
	"time"
)

func TestWorkspaces_GenerationLockDoesNotBlockOtherCards(t *testing.T) {
	w, err := NewWorkspaces(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Stands in for a card whose git subprocess hangs under its lock.
	unlock := w.lockGeneration("A", "")

	otherDone := make(chan error, 1)
	go func() {
		_, _, err := w.CreateForRun("B", "")
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("CreateForRun(B): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CreateForRun for another card blocked behind a held generation lock")
	}

	sameDone := make(chan error, 1)
	go func() {
		_, _, err := w.CreateForRun("A", "")
		sameDone <- err
	}()
	select {
	case <-sameDone:
		t.Fatal("CreateForRun for the locked generation did not wait for its lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-sameDone:
		if err != nil {
			t.Fatalf("CreateForRun(A): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CreateForRun(A) did not proceed after unlock")
	}

	w.locksMu.Lock()
	defer w.locksMu.Unlock()
	if len(w.locks) != 0 {
		t.Fatalf("generation locks leaked: %d left", len(w.locks))
	}
}
