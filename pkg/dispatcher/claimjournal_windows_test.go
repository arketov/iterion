//go:build windows

package dispatcher

import (
	"os"
	"testing"
	"time"
)

// A reader holding claims.json open (os.Open shares no DELETE) must not make
// persistLocked drop the rewrite: the journal is the crash-recovery record.
func TestClaimJournalPersistsWhileJournalFileIsHeldOpen(t *testing.T) {
	dir := t.TempDir()
	j := newClaimJournal(dir, quietLogger())
	j.Record(claimEntry{IssueID: "gh:1", Identifier: "gh#1", Marker: "rog-42", ClaimedAt: time.Now().UTC()})

	held, err := os.Open(j.path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(200 * time.Millisecond)
		held.Close()
	}()
	t.Cleanup(func() { <-released })

	j.Record(claimEntry{IssueID: "gh:2", Identifier: "gh#2", Marker: "rog-42", ClaimedAt: time.Now().UTC()})

	got := newClaimJournal(dir, quietLogger()).Load()
	if len(got) != 2 {
		t.Fatalf("reloaded journal = %+v, want both claims", got)
	}
}
