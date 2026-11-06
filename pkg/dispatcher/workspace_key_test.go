package dispatcher

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceKeyMaxLen is the longest directory name a workspace may have. Git on
// Windows refuses `git worktree add` for a gitdir of about 220 bytes, and the
// name is part of it (`<project>\.git\worktrees\<name>`), so the bound is a
// contract, not a taste: raising it needs a look at that failure.
const workspaceKeyMaxLen = 48

// the id the board hands the dispatcher for a native card.
const nativeCardID = "native:2cb294f3-b5f5-4636-a970-47307ebb541d"

func TestWorkspaceKeysStayShort(t *testing.T) {
	w, err := NewWorkspaces(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	ids := []string{
		nativeCardID,
		"github:SocialGouv/iterion#12345",
		strings.Repeat("долгий-идентификатор-", 20),
		strings.Repeat("a", 500),
		"x",
	}
	for _, id := range ids {
		for _, runID := range []string{"", "run_5f3c1d8e-0a52-4b9a-8f51-2f6c2f1b7a10"} {
			stable := filepath.Base(w.PathForRun(id, runID))
			ownerBase := strings.TrimSuffix(filepath.Base(w.ownerPathForRun(id, runID)), ".json")
			if ownerBase != stable {
				t.Errorf("marker name %q differs from directory name %q", ownerBase, stable)
			}
			if len(stable) > workspaceKeyMaxLen {
				t.Errorf("workspace name for %.30q run %q is %d characters (%q), want at most %d",
					id, runID, len(stable), stable, workspaceKeyMaxLen)
			}
		}
	}
	// The case the board hit: the old name was 100+ characters.
	short, legacy := workspaceKeys(nativeCardID, "")
	if len(legacy) <= 100 {
		t.Fatalf("fixture no longer covers the legacy long name: %d characters", len(legacy))
	}
	if len(short) > workspaceKeyMaxLen {
		t.Fatalf("short name %q is %d characters, want at most %d", short, len(short), workspaceKeyMaxLen)
	}
}

func TestWorkspaceKeysAreUnique(t *testing.T) {
	seen := map[string]string{}
	add := func(label, issueID, runID string) {
		t.Helper()
		short, _ := workspaceKeys(issueID, runID)
		if prev, dup := seen[short]; dup {
			t.Fatalf("%s and %s share the workspace name %q", prev, label, short)
		}
		seen[short] = label
	}
	// Same slug prefix (cut at workspaceKeySlugMax): only the digest differs.
	add("issue a", "native:2cb294f3-b5f5-4636-a970-000000000001", "")
	add("issue b", "native:2cb294f3-b5f5-4636-a970-000000000002", "")
	// Sanitization collisions.
	add("sanitize 1", "github:owner/repo#42", "")
	add("sanitize 2", "github/owner:repo#42", "")
	// One issue, several generations; the stable one is a generation of its own.
	add("stable", nativeCardID, "")
	add("run 1", nativeCardID, "run-1")
	add("run 2", nativeCardID, "run-2")
	// A run whose id spells the issue id must not alias the stable workspace.
	add("run named like the issue", nativeCardID, nativeCardID)
}

// makeLegacyGeneration lays down a generation the way the build before the short
// names did: the long key for its directory and its ownership marker.
func makeLegacyGeneration(t *testing.T, w *Workspaces, issueID, runID string, owner workspaceOwnerRecord) (dir, marker string) {
	t.Helper()
	_, legacy := workspaceKeys(issueID, runID)
	namespace := filepath.Join(w.Root(), workspaceKeyNamespace)
	if err := os.MkdirAll(filepath.Join(namespace, workspaceOwnersDir), 0o700); err != nil {
		t.Fatalf("mkdir legacy namespace: %v", err)
	}
	dir = filepath.Join(namespace, legacy)
	marker = filepath.Join(namespace, workspaceOwnersDir, legacy+".json")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir legacy workspace: %v", err)
	}
	owner.FormatVersion = 1
	owner.IssueID = issueID
	owner.RunID = runID
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal legacy marker: %v", err)
	}
	if err := os.WriteFile(marker, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy marker: %v", err)
	}
	return dir, marker
}

func readTestOwner(t *testing.T, path string) workspaceOwnerRecord {
	t.Helper()
	owner, err := readWorkspaceOwner(path)
	if err != nil {
		t.Fatalf("read marker %s: %v", path, err)
	}
	return owner
}

func samePath(t *testing.T, got, want string) {
	t.Helper()
	g, err1 := filepath.EvalSymlinks(got)
	wnt, err2 := filepath.EvalSymlinks(want)
	if err1 != nil || err2 != nil {
		t.Fatalf("resolve %q / %q: %v / %v", got, want, err1, err2)
	}
	if g != wnt {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// A workspace made under the long name keeps being found: its directory and its
// marker are used as they are, not orphaned next to a second, short-named one.
func TestLegacyLongNamedWorkspaceIsStillFound(t *testing.T) {
	for _, runID := range []string{"", "run-legacy"} {
		w := newSeedTestWorkspaces(t)
		dir, marker := makeLegacyGeneration(t, w, nativeCardID, runID, workspaceOwnerRecord{State: workspaceOwnerActive})
		// Content makes it a seeded workspace (an empty one under this marker is
		// the unseeded case, covered below).
		probe := writeSeedProbe(t, dir)
		if got := w.PathForRun(nativeCardID, runID); got != dir {
			t.Fatalf("run %q: PathForRun = %q, want the legacy directory %q", runID, got, dir)
		}
		if got := w.ownerPathForRun(nativeCardID, runID); got != marker {
			t.Fatalf("run %q: ownerPathForRun = %q, want the legacy marker %q", runID, got, marker)
		}
		generation, exists, managed, err := w.resumeGenerationState(nativeCardID, runID)
		if err != nil || !exists || !managed || generation != runID {
			t.Fatalf("run %q: resumeGenerationState = (%q, %t, %t, %v), want the legacy generation managed", runID, generation, exists, managed, err)
		}
		path, created, err := w.CreateForRun(nativeCardID, runID)
		if err != nil || created {
			t.Fatalf("run %q: CreateForRun = (%q, created=%t, %v), want the existing workspace", runID, path, created, err)
		}
		samePath(t, path, dir)
		if _, err := os.Stat(probe); err != nil {
			t.Fatalf("run %q: legacy workspace lost its content: %v", runID, err)
		}
		short, _ := workspaceKeys(nativeCardID, runID)
		if _, err := os.Lstat(filepath.Join(w.Root(), workspaceKeyNamespace, short)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("run %q: a second, short-named workspace was created beside the legacy one (err=%v)", runID, err)
		}
	}
}

// A seeded legacy workspace keeps its marker: the seeding bookkeeping reads and
// writes it where it is.
func TestLegacyWorkspaceKeepsItsSeedMarkers(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "run-seeded", workspaceOwnerRecord{
		State:       workspaceOwnerActive,
		SeedTracked: true,
	})
	probe := writeSeedProbe(t, dir)

	path, created, reseeded, err := w.CreateForDispatch(nativeCardID, "run-seeded")
	if err != nil || created || reseeded {
		t.Fatalf("CreateForDispatch = (%q, created=%t, reseeded=%t, %v), want the seeded legacy workspace as is", path, created, reseeded, err)
	}
	samePath(t, path, dir)
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("seeded legacy workspace lost its content: %v", err)
	}

	// A marker that predates seed_tracked is reported untracked and upgraded by
	// ClearSeedPending, in the legacy file.
	oldDir, oldMarker := makeLegacyGeneration(t, w, nativeCardID, "run-old", workspaceOwnerRecord{State: workspaceOwnerActive})
	writeSeedProbe(t, oldDir)
	if !w.SeedUntracked(nativeCardID, "run-old") {
		t.Fatal("a legacy marker without seed_tracked must be reported untracked")
	}
	if err := w.ClearSeedPending(nativeCardID, "run-old"); err != nil {
		t.Fatalf("ClearSeedPending: %v", err)
	}
	if owner := readTestOwner(t, oldMarker); owner.SeedPending || !owner.SeedTracked {
		t.Fatalf("legacy marker after ClearSeedPending = %+v, want seed_pending=false seed_tracked=true", owner)
	}
	if w.SeedUntracked(nativeCardID, "run-old") {
		t.Fatal("SeedUntracked must read the upgraded legacy marker")
	}
	if got := readTestOwner(t, marker); got.SeedPending || !got.SeedTracked {
		t.Fatalf("an unrelated legacy marker changed: %+v", got)
	}
}

// The live case: a legacy generation whose seeding never finished cannot be
// re-seeded under its long name (git refuses it), so it is discarded and the
// dispatch gets a fresh workspace under the short name, reported as reseeded so
// after_create runs again.
func TestLegacyUnseededWorkspaceMovesToTheShortName(t *testing.T) {
	cases := []struct {
		name  string
		owner workspaceOwnerRecord
		files bool // leave a half-run seed in the directory
	}{
		{"pending-half", workspaceOwnerRecord{State: workspaceOwnerActive, SeedPending: true, SeedTracked: true}, true},
		{"pending-empty", workspaceOwnerRecord{State: workspaceOwnerActive, SeedPending: true, SeedTracked: true}, false},
		{"unmarked-empty", workspaceOwnerRecord{State: workspaceOwnerActive}, false},
	}
	for _, tc := range cases {
		for _, runID := range []string{"", "run-bars"} {
			// Short subtest names: the test's temp directory carries them, and the
			// legacy names are already long.
			t.Run(tc.name+"-"+strings.TrimPrefix(runID, "run-"), func(t *testing.T) {
				w := newSeedTestWorkspaces(t)
				w.SetSeedConfigured(func() bool { return true })
				dir, marker := makeLegacyGeneration(t, w, nativeCardID, runID, tc.owner)
				if tc.files {
					writeSeedProbe(t, dir)
				}
				short, _ := workspaceKeys(nativeCardID, runID)
				if got := filepath.Base(w.PathForRun(nativeCardID, runID)); got != short {
					t.Fatalf("an unseeded legacy workspace resolves to %q, want the short %q", got, short)
				}

				path, created, reseeded, err := w.CreateForDispatch(nativeCardID, runID)
				if err != nil || !created || !reseeded {
					t.Fatalf("CreateForDispatch = (%q, created=%t, reseeded=%t, %v), want a fresh reseeded workspace", path, created, reseeded, err)
				}
				if got := filepath.Base(path); got != short {
					t.Fatalf("workspace is named %q, want %q", got, short)
				}
				for _, p := range []string{dir, marker} {
					if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("legacy leftover %s survived (err=%v)", p, err)
					}
				}
				if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
					t.Fatalf("new workspace is not empty: %v, %v", entries, err)
				}
				if owner := readTestOwner(t, w.ownerPathForRun(nativeCardID, runID)); !owner.SeedPending || !owner.SeedTracked {
					t.Fatalf("new marker = %+v, want seed_pending and seed_tracked", owner)
				}

				// The seed finishes: the next dispatch gets the workspace as is.
				if err := w.ClearSeedPending(nativeCardID, runID); err != nil {
					t.Fatalf("ClearSeedPending: %v", err)
				}
				if _, created, reseeded, err := w.CreateForDispatch(nativeCardID, runID); err != nil || created || reseeded {
					t.Fatalf("second CreateForDispatch: created=%t reseeded=%t err=%v, want the seeded workspace as is", created, reseeded, err)
				}
			})
		}
	}
}

// Without an after_create hook an empty directory under an unmarked legacy
// workspace is no failed seed (a `worktree: auto` run works in its own
// checkout): the generation stays under its legacy name and stays resumable.
func TestLegacyEmptyWorkspaceWithoutSeedHookStaysResumable(t *testing.T) {
	w := newSeedTestWorkspaces(t) // no SetSeedConfigured: no hook
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "run-auto", workspaceOwnerRecord{State: workspaceOwnerActive})
	if got := w.PathForRun(nativeCardID, "run-auto"); got != dir {
		t.Fatalf("PathForRun = %q, want the legacy directory %q", got, dir)
	}
	generation, exists, managed, err := w.resumeGenerationState(nativeCardID, "run-auto")
	if err != nil || !exists || !managed || generation != "run-auto" {
		t.Fatalf("resumeGenerationState = (%q, %t, %t, %v), want the legacy generation, managed", generation, exists, managed, err)
	}
	path, created, reseeded, err := w.CreateForDispatch(nativeCardID, "run-auto")
	if err != nil || created || reseeded {
		t.Fatalf("CreateForDispatch = (%q, created=%t, reseeded=%t, %v), want the legacy workspace untouched", path, created, reseeded, err)
	}
	samePath(t, path, dir)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("legacy marker is gone: %v", err)
	}

	// The same shape with the hook configured is the failed-seed leftover.
	w.SetSeedConfigured(func() bool { return true })
	if got, short := filepath.Base(w.PathForRun(nativeCardID, "run-auto")), mustShortKey(nativeCardID, "run-auto"); got != short {
		t.Fatalf("with a hook the empty legacy workspace resolves to %q, want %q", got, short)
	}
}

func mustShortKey(issueID, runID string) string {
	short, _ := workspaceKeys(issueID, runID)
	return short
}

// A discard interrupted between removing the directory and the marker leaves a
// seed-pending marker alone; the next dispatch finishes the job instead of
// refusing the generation as "owned target is missing".
func TestLegacyDiscardInterruptedBeforeMarkerRemovalIsRetried(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "run-half", workspaceOwnerRecord{
		State: workspaceOwnerActive, SeedPending: true, SeedTracked: true,
	})
	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove legacy directory: %v", err)
	}
	path, created, reseeded, err := w.CreateForDispatch(nativeCardID, "run-half")
	if err != nil || !created || !reseeded {
		t.Fatalf("CreateForDispatch = (%q, created=%t, reseeded=%t, %v)", path, created, reseeded, err)
	}
	short, _ := workspaceKeys(nativeCardID, "run-half")
	if got := filepath.Base(path); got != short {
		t.Fatalf("workspace is named %q, want %q", got, short)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy marker survived (err=%v)", err)
	}
}

// Content under a legacy marker that says nothing about seeding is somebody's
// work: it is never discarded.
func TestLegacyWorkspaceWithContentIsNeverDiscarded(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "", workspaceOwnerRecord{State: workspaceOwnerActive})
	probe := writeSeedProbe(t, dir)
	path, created, reseeded, err := w.CreateForDispatch(nativeCardID, "")
	if err != nil || created || reseeded {
		t.Fatalf("CreateForDispatch = (%q, created=%t, reseeded=%t, %v), want the workspace untouched", path, created, reseeded, err)
	}
	for _, p := range []string{probe, marker} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s is gone: %v", p, err)
		}
	}
}

// Retire and remove work on the legacy names, and once the generation is gone a
// new one with the same ids gets the short name.
func TestLegacyWorkspaceRetireAndRemove(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "run-gone", workspaceOwnerRecord{State: workspaceOwnerActive, SeedTracked: true})

	if err := w.RetireForRun(nativeCardID, "run-gone"); err != nil {
		t.Fatalf("RetireForRun: %v", err)
	}
	if got := readTestOwner(t, marker).State; got != workspaceOwnerRetired {
		t.Fatalf("legacy marker state = %q, want retired", got)
	}
	if err := w.RemoveForRun(nativeCardID, "run-gone"); err != nil {
		t.Fatalf("RemoveForRun: %v", err)
	}
	for _, p := range []string{dir, marker} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived RemoveForRun (err=%v)", p, err)
		}
	}
	short, _ := workspaceKeys(nativeCardID, "run-gone")
	if got := filepath.Base(w.PathForRun(nativeCardID, "run-gone")); got != short {
		t.Fatalf("after removal the name is %q, want the short %q", got, short)
	}
}

// Only the marker of a legacy generation is left (its directory was removed by
// hand): the generation still resolves to the legacy key, so the fail-closed
// "owned target is missing" refusal is kept instead of a silent fresh start.
func TestLegacyMarkerAloneKeepsTheLegacyKey(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	dir, marker := makeLegacyGeneration(t, w, nativeCardID, "run-orphan", workspaceOwnerRecord{State: workspaceOwnerActive})
	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove legacy directory: %v", err)
	}
	if got := w.ownerPathForRun(nativeCardID, "run-orphan"); got != marker {
		t.Fatalf("ownerPathForRun = %q, want the legacy marker %q", got, marker)
	}
	if _, _, err := w.CreateForRun(nativeCardID, "run-orphan"); err == nil ||
		!strings.Contains(err.Error(), "is missing while marker") {
		t.Fatalf("CreateForRun over an orphan legacy marker = %v, want the fail-closed refusal", err)
	}
}

// A new generation, and one next to legacy generations of other runs, is made
// under the short name.
func TestNewWorkspacesUseTheShortName(t *testing.T) {
	w := newSeedTestWorkspaces(t)
	makeLegacyGeneration(t, w, nativeCardID, "run-old", workspaceOwnerRecord{State: workspaceOwnerActive})

	path, created, _, err := w.CreateForDispatch(nativeCardID, "run-new")
	if err != nil || !created {
		t.Fatalf("CreateForDispatch = (%q, created=%t, %v)", path, created, err)
	}
	short, _ := workspaceKeys(nativeCardID, "run-new")
	if got := filepath.Base(path); got != short {
		t.Fatalf("new workspace is named %q, want %q", got, short)
	}
	if len(filepath.Base(path)) > workspaceKeyMaxLen {
		t.Fatalf("new workspace name %q is longer than %d", filepath.Base(path), workspaceKeyMaxLen)
	}
	marker := w.ownerPathForRun(nativeCardID, "run-new")
	if filepath.Base(marker) != short+".json" {
		t.Fatalf("new marker is %q, want %q", filepath.Base(marker), short+".json")
	}
	if owner := readTestOwner(t, marker); !owner.SeedPending || !owner.SeedTracked {
		t.Fatalf("new marker = %+v, want seed_pending and seed_tracked", owner)
	}
}
