package dispatcher

import (
	"os"
	"path/filepath"
	"testing"
)

func newSeedTestWorkspaces(t *testing.T) *Workspaces {
	t.Helper()
	ws, err := NewWorkspaces(filepath.Join(t.TempDir(), "ws"))
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	return ws
}

func writeSeedProbe(t *testing.T, dir string) string {
	t.Helper()
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	return probe
}

// A workspace whose seeding never finished (after_create failed or the
// daemon died in it) is recreated empty on the next dispatch and reported as
// created, so after_create runs again — the bot is never started in it as is.
func TestCreateForDispatchReseedsPendingWorkspace(t *testing.T) {
	ws := newSeedTestWorkspaces(t)

	path, created, reseeded, err := ws.CreateForDispatch("i1", "")
	if err != nil || !created || reseeded {
		t.Fatalf("first create: created=%v reseeded=%v err=%v", created, reseeded, err)
	}
	probe := writeSeedProbe(t, path) // what a half-run seed leaves behind

	path2, created, reseeded, err := ws.CreateForDispatch("i1", "")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if !created || !reseeded {
		t.Fatalf("a still-pending workspace must come back created+reseeded, got created=%v reseeded=%v", created, reseeded)
	}
	if path2 != path {
		t.Fatalf("path changed: %q -> %q", path, path2)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Fatalf("leftover of the failed seed survived the reset (stat err=%v)", err)
	}

	// The seed finally succeeds: the workspace is the bot's from now on.
	if err := ws.ClearSeedPending("i1", ""); err != nil {
		t.Fatalf("ClearSeedPending: %v", err)
	}
	probe = writeSeedProbe(t, path)
	_, created, reseeded, err = ws.CreateForDispatch("i1", "")
	if err != nil || created || reseeded {
		t.Fatalf("a seeded workspace must be handed back as is: created=%v reseeded=%v err=%v", created, reseeded, err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("a seeded workspace lost its content: %v", err)
	}
}

// A workspace made by the plain CreateForRun (or by a build before the
// seed-pending mark) carries no mark and is taken as seeded.
func TestCreateForDispatchKeepsAWorkspaceWithoutThePendingMark(t *testing.T) {
	ws := newSeedTestWorkspaces(t)
	path, created, err := ws.CreateForRun("i2", "")
	if err != nil || !created {
		t.Fatalf("CreateForRun: created=%v err=%v", created, err)
	}
	probe := writeSeedProbe(t, path)

	_, created, reseeded, err := ws.CreateForDispatch("i2", "")
	if err != nil || created || reseeded {
		t.Fatalf("created=%v reseeded=%v err=%v, want the workspace untouched", created, reseeded, err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("content of an unmarked workspace was wiped: %v", err)
	}
}

// Generation-owned (cleanup-policy) workspaces follow the same rule.
func TestCreateForDispatchReseedsPendingGenerationWorkspace(t *testing.T) {
	ws := newSeedTestWorkspaces(t)
	path, created, _, err := ws.CreateForDispatch("i3", "run-1")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	writeSeedProbe(t, path)
	_, created, reseeded, err := ws.CreateForDispatch("i3", "run-1")
	if err != nil || !created || !reseeded {
		t.Fatalf("created=%v reseeded=%v err=%v, want a reset", created, reseeded, err)
	}
}

func TestClearSeedPendingIsANoopWithoutAMarker(t *testing.T) {
	ws := newSeedTestWorkspaces(t)
	if err := ws.ClearSeedPending("never-created", ""); err != nil {
		t.Fatalf("ClearSeedPending without a marker: %v", err)
	}
	if err := ws.ClearSeedPending("", ""); err == nil {
		t.Fatal("ClearSeedPending with no issue id must fail")
	}
	// Twice on a real marker is fine too.
	if _, _, _, err := ws.CreateForDispatch("i4", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ws.ClearSeedPending("i4", ""); err != nil {
			t.Fatalf("ClearSeedPending #%d: %v", i, err)
		}
	}
}

func TestDirIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if !dirIsEmpty(dir) {
		t.Error("an empty directory must read as empty")
	}
	writeSeedProbe(t, dir)
	if dirIsEmpty(dir) {
		t.Error("a directory with a file in it must not read as empty")
	}
	if dirIsEmpty(filepath.Join(dir, "missing")) {
		t.Error("a path that cannot be read must not read as empty")
	}
}

// A marker written by the plain CreateForRun (or by a build before the
// seed-pending mark) is "untracked": an empty directory under it may be an
// unseeded leftover. CreateForDispatch's own markers are tracked from the
// start, and ClearSeedPending upgrades an untracked one.
func TestSeedUntracked(t *testing.T) {
	ws := newSeedTestWorkspaces(t)
	if ws.SeedUntracked("nobody", "") {
		t.Error("no marker is not an untracked marker")
	}
	if _, _, err := ws.CreateForRun("old", ""); err != nil {
		t.Fatal(err)
	}
	if !ws.SeedUntracked("old", "") {
		t.Error("a CreateForRun marker must read as untracked")
	}
	if err := ws.ClearSeedPending("old", ""); err != nil {
		t.Fatal(err)
	}
	if ws.SeedUntracked("old", "") {
		t.Error("ClearSeedPending must upgrade an untracked marker")
	}
	if _, _, _, err := ws.CreateForDispatch("new", ""); err != nil {
		t.Fatal(err)
	}
	if ws.SeedUntracked("new", "") {
		t.Error("a CreateForDispatch marker is tracked from the start")
	}
}
