package native

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// isolatedStores opens two stores on one root with no watch and no rescan
// net: the first is the "studio server", the second the "CLI process". A
// write through the second is invisible to the first unless the first asks
// the disk — the failure the board-sync card reports.
func isolatedStores(t *testing.T) (server, cli *Store) {
	t.Helper()
	refuseWatch(t)
	setRescanInterval(t, 0)
	dir := t.TempDir()
	server, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (server): %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	cli, err = NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (cli): %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return server, cli
}

func indexed(s *Store, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.index[id]
	return ok
}

// rewriteTitleOnDisk edits a card file the way another process's atomic
// write would, behind the server store's back.
func rewriteTitleOnDisk(t *testing.T, s *Store, id, title string) {
	t.Helper()
	data, err := os.ReadFile(s.issuePath(id))
	if err != nil {
		t.Fatalf("read card: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse card: %v", err)
	}
	m["title"] = title
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	if err := os.WriteFile(s.issuePath(id), out, filePerm); err != nil {
		t.Fatalf("write card: %v", err)
	}
}

// A by-id lookup of a card the index never heard of must ask the disk:
// GET /api/v1/native/issues/<id> answered 404 for a card that was on disk.
func TestGet_AdoptsACardAnotherProcessWrote(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "From the CLI", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if indexed(server, created.ID) {
		t.Fatalf("fixture: the server index already knows %s", created.ID)
	}
	got, err := server.Get(created.ID)
	if err != nil {
		t.Fatalf("Get a card that is on disk: %v", err)
	}
	if got.Title != "From the CLI" {
		t.Errorf("title = %q", got.Title)
	}
	if !indexed(server, created.ID) {
		t.Error("Get answered from disk but did not index the card")
	}
}

func TestGet_UnknownIDStaysNotFound(t *testing.T) {
	server, _ := isolatedStores(t)
	for _, id := range []string{
		"native:7f2c1d0e-0000-4000-8000-000000000000", // well-formed, no file
		"../board", // not a card id: must never reach the filesystem
		"native:not-a-uuid",
		"",
	} {
		if _, err := server.Get(id); err == nil {
			t.Errorf("Get(%q) = nil error, want not found", id)
		}
	}
}

func TestResolve_FindsACardAnotherProcessWrote(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "Prefix me", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prefix := strings.TrimPrefix(created.ID, "native:")[:8]
	got, err := server.Resolve(prefix)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", prefix, err)
	}
	if got != created.ID {
		t.Errorf("Resolve = %q, want %q", got, created.ID)
	}
}

func TestRefreshIndex_ComparesTheDirectoryUnlessForced(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "Before", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Same set of files, one of them changed: the cheap check sees nothing.
	if err := server.RefreshIndex(false); err != nil {
		t.Fatalf("RefreshIndex(false): %v", err) // adopts the new card (set differs)
	}
	if !indexed(server, created.ID) {
		t.Fatal("RefreshIndex(false) missed a card that is on disk but not in the index")
	}
	rewriteTitleOnDisk(t, server, created.ID, "After")
	if err := server.RefreshIndex(false); err != nil {
		t.Fatalf("RefreshIndex(false): %v", err)
	}
	if got, _ := server.Get(created.ID); got.Title != "Before" {
		t.Fatalf("a drift-only check re-read card contents: title = %q", got.Title)
	}

	// Forced: every card is re-read.
	if err := server.RefreshIndex(true); err != nil {
		t.Fatalf("RefreshIndex(true): %v", err)
	}
	if got, _ := server.Get(created.ID); got.Title != "After" {
		t.Errorf("forced refresh kept the stale title %q", got.Title)
	}

	// A card deleted behind the index's back is a drift too.
	if err := os.Remove(server.issuePath(created.ID)); err != nil {
		t.Fatal(err)
	}
	if err := server.RefreshIndex(false); err != nil {
		t.Fatalf("RefreshIndex(false): %v", err)
	}
	if indexed(server, created.ID) {
		t.Error("RefreshIndex(false) left a tombstone for a card whose file is gone")
	}
}

// A REMOVE event is what Windows reports for the name an atomic replace
// overwrites. The card is still there — dropping it on the op alone was the
// "card disappears after issue update" symptom.
func TestApplyEvent_RemoveOpForAFileThatStillExistsKeepsTheCard(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "Replaced", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := server.RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	rewriteTitleOnDisk(t, server, created.ID, "Replaced again")

	applyEvent(server, created.ID, fsnotify.Remove)

	got, err := server.Get(created.ID)
	if err != nil {
		t.Fatalf("card dropped by a REMOVE for a file that exists: %v", err)
	}
	if got.Title != "Replaced again" {
		t.Errorf("title = %q, want the on-disk copy", got.Title)
	}
}

func TestApplyEvent_RemoveOpForAGoneFileDropsTheCard(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "Gone", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := server.RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(server.issuePath(created.ID)); err != nil {
		t.Fatal(err)
	}
	applyEvent(server, created.ID, fsnotify.Remove)
	if indexed(server, created.ID) {
		t.Error("a REMOVE for a file that is gone left the card indexed")
	}
}

// A file that exists but will not read (the sharing violation a Windows
// writer's handle causes) keeps the last indexed copy and is retried.
func TestApplyEvent_UnreadableFileKeepsTheLastCopyThenHeals(t *testing.T) {
	server, cli := isolatedStores(t)
	created, err := cli.Create(Issue{Title: "Stable", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := server.RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(server.issuePath(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Half-written file: unparsable now, valid after the "writer" finishes.
	if err := os.WriteFile(server.issuePath(created.ID), good[:len(good)/2], filePerm); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(server.issuePath(created.ID), []byte(strings.Replace(string(good), "Stable", "Healed", 1)), filePerm)
	}()
	applyEvent(server, created.ID, fsnotify.Create)
	<-done

	got, err := server.Get(created.ID)
	if err != nil {
		t.Fatalf("card lost while its file was unreadable: %v", err)
	}
	if got.Title != "Healed" {
		t.Errorf("title = %q: the retry did not pick up the finished write", got.Title)
	}
}

// The verification net is what makes an armed watch trustworthy: an event
// that never came (simulated by dropping the entry) is repaired by the
// periodic reconcile.
func TestWatcher_VerificationNetRepairsALostEvent(t *testing.T) {
	setSeam(t, &verifyIntervalOverride, 50*time.Millisecond)
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if w, _, _ := s.watchState(); w == nil {
		t.Skip("host refused the fsnotify watch; the rescan net covers this host")
	}
	created, err := s.Create(Issue{Title: "Lost event", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.mu.Lock()
	s.dropIndexLocked(created.ID) // the event that never arrived
	s.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for !indexed(s, created.ID) {
		if time.Now().After(deadline) {
			t.Fatal("the verification net never re-indexed a card that is on disk")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestVerifyInterval_EnvSpellings(t *testing.T) {
	cases := map[string]time.Duration{
		"":      defaultVerifyInterval,
		"off":   0,
		"0":     0,
		"3":     3 * time.Second,
		"750ms": 750 * time.Millisecond,
		"2 s":   defaultVerifyInterval, // not a spelling: never read as "off"
	}
	for raw, want := range cases {
		t.Setenv("ITERION_NATIVE_INDEX_VERIFY", raw)
		if got := verifyInterval(); got != want {
			t.Errorf("ITERION_NATIVE_INDEX_VERIFY=%q: interval = %v, want %v", raw, got, want)
		}
	}
}

// The Board's list endpoint reconciles on a directory/index mismatch, and its
// ?refresh=1 re-reads every card.
func TestListEndpoint_SeesExternalWritesAndRefreshRereads(t *testing.T) {
	server, cli := isolatedStores(t)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux, "/api/v1/native")

	list := func(query string) []Issue {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/native/issues"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET issues%s = %d: %s", query, rec.Code, rec.Body.String())
		}
		var out []Issue
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	created, err := cli.Create(Issue{Title: "Written by the CLI", State: "backlog"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := list("")
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("plain list = %+v, want the card the CLI wrote", got)
	}

	rewriteTitleOnDisk(t, server, created.ID, "Edited by the CLI")
	if got := list(""); got[0].Title != "Written by the CLI" {
		t.Fatalf("a plain list re-read contents: %q", got[0].Title)
	}
	if got := list("?refresh=1"); got[0].Title != "Edited by the CLI" {
		t.Errorf("refresh=1 list kept the stale title %q", got[0].Title)
	}
}
