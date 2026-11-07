//go:build windows

package dispatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newMarkerForRenameTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marker.json")
	rec := workspaceOwnerRecord{FormatVersion: 1, IssueID: "I", State: workspaceOwnerActive, SeedPending: true, SeedTracked: true}
	if err := createWorkspaceOwner(path, rec); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReplaceWorkspaceOwner_RetriesWhileMarkerIsOpen(t *testing.T) {
	path := newMarkerForRenameTest(t)
	// A plain os.Open has no FILE_SHARE_DELETE: while it is open, a rename over
	// the file fails.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.Close()
	}()
	next := workspaceOwnerRecord{FormatVersion: 1, IssueID: "I", State: workspaceOwnerActive, SeedTracked: true}
	if err := replaceWorkspaceOwner(path, next); err != nil {
		t.Fatalf("replaceWorkspaceOwner while the marker is briefly open: %v", err)
	}
	got, err := readWorkspaceOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SeedPending {
		t.Fatalf("marker still seed-pending after replace: %+v", got)
	}
}

func TestReplaceWorkspaceOwner_GivesUpWhenMarkerStaysOpen(t *testing.T) {
	path := newMarkerForRenameTest(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	next := workspaceOwnerRecord{FormatVersion: 1, IssueID: "I", State: workspaceOwnerActive, SeedTracked: true}
	if err := replaceWorkspaceOwner(path, next); err == nil {
		t.Fatal("replaceWorkspaceOwner succeeded while the marker stayed open")
	}
	got, err := readWorkspaceOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SeedPending {
		t.Fatalf("old marker was changed: %+v", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".owner-atomic-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
