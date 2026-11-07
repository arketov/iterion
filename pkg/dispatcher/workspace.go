package dispatcher

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

// Workspaces manages per-issue workspace directories under a single
// root. It enforces filename sanitization and refuses to traverse
// outside the root via symlinks or pathological IDs. Operations are serialised
// per issue/run generation (lockGeneration), never globally, so a git call that
// hangs in one card's workspace does not delay other cards. A Workspaces must
// not be copied.
type Workspaces struct {
	root string
	// locks holds one refcounted lock per generation in use; guarded by locksMu.
	locksMu sync.Mutex
	locks   map[string]*generationLock
	// seedConfigured reports whether the dispatcher runs an after_create hook.
	// Unset (the zero value) means it does not. Only the move of a long-named
	// legacy workspace to the short name reads it (see legacyIsUnseeded).
	seedConfigured atomic.Pointer[func() bool]
}

// SetSeedConfigured tells the manager how to ask whether the dispatcher seeds
// a fresh workspace with an after_create hook. The dispatcher sets it when it
// is built; the answer is read at use time, so a config reload is followed.
func (w *Workspaces) SetSeedConfigured(fn func() bool) {
	if fn == nil {
		w.seedConfigured.Store(nil)
		return
	}
	w.seedConfigured.Store(&fn)
}

type generationLock struct {
	mu   sync.Mutex
	refs int
}

// lockGeneration serialises operations on one issue/run generation and returns
// the unlock func. The short and the legacy directory name of a generation share
// one lock; different generations never wait for each other.
func (w *Workspaces) lockGeneration(issueID, runID string) (unlock func()) {
	key, _ := workspaceKeys(issueID, runID)
	w.locksMu.Lock()
	if w.locks == nil {
		w.locks = make(map[string]*generationLock)
	}
	gl := w.locks[key]
	if gl == nil {
		gl = &generationLock{}
		w.locks[key] = gl
	}
	gl.refs++
	w.locksMu.Unlock()
	gl.mu.Lock()
	return func() {
		gl.mu.Unlock()
		w.locksMu.Lock()
		gl.refs--
		if gl.refs == 0 {
			delete(w.locks, key)
		}
		w.locksMu.Unlock()
	}
}

func (w *Workspaces) seedHookConfigured() bool {
	fn := w.seedConfigured.Load()
	return fn != nil && (*fn)()
}

// workspaceKeyNamespace deliberately starts with a dot. sanitizeKey never
// returns a leading-dot name, so this namespace cannot alias a workspace
// created by versions that used filepath.Join(root, sanitizeKey(issueID)).
//
// We do not automatically adopt or rename those legacy directories: the old
// sanitization was many-to-one, so their owning issue cannot be proven from
// the directory name alone. Leaving them untouched is the only fail-closed
// migration behaviour; new workspaces are allocated in this disjoint
// namespace.
const workspaceKeyNamespace = ".issue-workspaces-v2"

// A workspace directory name is `<slug>--<digest>`: a readable prefix cut to
// workspaceKeySlugMax characters and the first workspaceKeyDigestHex hex digits
// of a SHA-256 over the full identity. The name stays short on purpose. `git
// worktree add <path>` registers the checkout under `.git/worktrees/<basename
// of path>`, and git on Windows refuses a gitdir of about 220 bytes or more
// ("'$GIT_DIR' too big", then "Filename too long"; core.longpaths does not
// lift it). The project's own folder, `\.git\worktrees\` and this name all count
// toward that limit, and the folder is the operator's to pick, so the name
// takes as little of it as identity allows.
//
// The digest is the identity-bearing part. 64 bits keep two live generations
// from sharing a name in any real deployment, and a collision still fails
// closed: the ownership marker records the issue and run and is verified
// before a directory is adopted.
const (
	workspaceKeySlugMax   = 24
	workspaceKeyDigestHex = 16
)

// legacyWorkspaceKeySlugMax is the slug length of the first v2 key format,
// `<slug up to 80>--<full 64 hex digits>` (up to 146 characters). Directories
// and ownership markers made under it are still found (see keyFor) but never
// created again.
const legacyWorkspaceKeySlugMax = 80

const workspaceOwnersDir = ".owners"

type workspaceOwnerState string

const (
	workspaceOwnerActive  workspaceOwnerState = "active"
	workspaceOwnerRetired workspaceOwnerState = "retired"
)

type workspaceOwnerRecord struct {
	FormatVersion int                 `json:"format_version"`
	IssueID       string              `json:"issue_id"`
	RunID         string              `json:"run_id,omitempty"`
	State         workspaceOwnerState `json:"state"`
	// SeedPending is true from the moment CreateForDispatch creates the
	// directory until ClearSeedPending says the seeding step (after_create)
	// finished. A directory still marked pending on the next dispatch is the
	// leftover of an after_create that failed or was interrupted: it is
	// recreated empty and seeded again instead of being handed to the bot
	// half-built. Absent (false) on markers written before this field and on
	// those made by CreateForRun: such a directory is taken as seeded.
	SeedPending bool `json:"seed_pending,omitempty"`
	// SeedTracked is true on a marker written by CreateForDispatch (and after
	// ClearSeedPending): its SeedPending is then the whole truth. A marker
	// without it predates the seed-pending mark, so an empty directory under it
	// may be the leftover of a failed after_create (SeedUntracked).
	SeedTracked bool `json:"seed_tracked,omitempty"`
}

// NewWorkspaces returns a manager rooted at the given path. The root
// itself is created on first Create.
func NewWorkspaces(root string) (*Workspaces, error) {
	if root == "" {
		return nil, errors.New("workspace: root path required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve root: %w", err)
	}
	return &Workspaces{root: abs}, nil
}

// Root returns the absolute root path.
func (w *Workspaces) Root() string { return w.root }

// Path returns the stable, versioned workspace path for an issue whose persist
// policy intentionally reuses one workspace across dispatches.
func (w *Workspaces) Path(issueID string) string {
	return w.PathForRun(issueID, "")
}

// PathForRun returns a generation-specific path for one logical dispatch.
// The readable slug is not an identity boundary: a SHA-256 digest of the
// original issue ID and generation separates sanitization collisions. A
// completed dispatch's absolute path is never reused, so stale background
// writers remain isolated from later dispatches.
func (w *Workspaces) PathForRun(issueID, runID string) string {
	return filepath.Join(w.root, workspaceKeyNamespace, w.keyFor(issueID, runID))
}

// Create ensures the stable workspace for issueID exists. It is retained for
// persist=keep and for API compatibility; cleanup policies use CreateForRun.
func (w *Workspaces) Create(issueID string) (path string, created bool, err error) {
	return w.CreateForRun(issueID, "")
}

// CreateForRun ensures a generation-owned workspace exists. The boolean
// reports whether this call created it, so callers run after_create exactly
// once. The returned path is guaranteed to remain under the configured root.
func (w *Workspaces) CreateForRun(issueID, runID string) (path string, created bool, err error) {
	path, created, _, err = w.createForRun(issueID, runID, false)
	return path, created, err
}

// CreateForDispatch is CreateForRun for a dispatch that seeds the workspace
// (after_create): the directory it creates is marked seed-pending until the
// caller reports the seeding done with ClearSeedPending. A directory still
// pending from an earlier dispatch — its after_create failed, or the daemon
// died in it — is emptied and recreated, and created and reseeded are both
// true: the caller runs after_create again and the bot never starts in a
// half-seeded or empty folder. Only a directory this dispatcher created and
// never declared seeded is reset; one without the pending mark is left alone.
func (w *Workspaces) CreateForDispatch(issueID, runID string) (path string, created, reseeded bool, err error) {
	return w.createForRun(issueID, runID, true)
}

// ClearSeedPending records that the workspace's seeding step finished: the
// next CreateForDispatch for this generation hands it back as is. A no-op for
// an absent marker or one already recorded as seeded, so it is safe to call
// after every seeding step, with or without an after_create hook.
func (w *Workspaces) ClearSeedPending(issueID, runID string) error {
	if issueID == "" {
		return errors.New("workspace: issue id required")
	}
	defer w.lockGeneration(issueID, runID)()

	ownerPath := w.ownerPathForRun(issueID, runID)
	owner, err := readWorkspaceOwner(ownerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("workspace: read ownership marker %s: %w", ownerPath, err)
	}
	if err := verifyWorkspaceOwnerID(owner, issueID, runID); err != nil {
		return fmt.Errorf("workspace: verify ownership marker %s: %w", ownerPath, err)
	}
	if !owner.SeedPending && owner.SeedTracked {
		return nil
	}
	// Also upgrades a marker that predates the mark: from here on its
	// SeedPending is the whole truth (see SeedTracked).
	owner.SeedPending = false
	owner.SeedTracked = true
	if err := replaceWorkspaceOwner(ownerPath, owner); err != nil {
		return fmt.Errorf("workspace: record %s as seeded: %w", ownerPath, err)
	}
	return nil
}

// SeedUntracked reports whether the workspace's ownership marker predates the
// seed-pending mark (it was written by a build that did not track seeding). An
// empty directory under such a marker is the one case where a failed
// after_create cannot be told apart by the marker itself.
func (w *Workspaces) SeedUntracked(issueID, runID string) bool {
	defer w.lockGeneration(issueID, runID)()
	owner, err := readWorkspaceOwner(w.ownerPathForRun(issueID, runID))
	return err == nil && owner.State == workspaceOwnerActive && !owner.SeedTracked
}

// dirIsEmpty reports whether path is a directory with no entry in it. A path
// that cannot be read is not reported empty.
func dirIsEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

// discardUnseededTarget deletes an unseeded workspace directory and deregisters
// the git worktree the seed step may have registered for it. An absent
// directory is fine. The caller holds the generation lock and has verified ownership.
func discardUnseededTarget(target string) error {
	gitCommonDir, _ := workspaceGitCommonDir(target)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("workspace: discard unseeded workspace %s: %w — a process still holds a file in it "+
			"(OneDrive, an antivirus, a process the hook left running): close it, or delete the folder by hand", target, err)
	}
	if gitCommonDir == "" {
		return nil
	}
	if err := removeGitWorktreeRegistration(gitCommonDir, target); err != nil {
		return fmt.Errorf("workspace: discard unseeded workspace %s: %w (run `git worktree prune` in the host repository)", target, err)
	}
	return nil
}

// resetUnseededTarget empties a seed-pending workspace and recreates the bare
// directory: the leftover of a failed after_create. A git worktree the seed
// step registered for the path is deregistered, so the next seed can add it
// again. The caller holds the generation lock and has verified ownership.
func resetUnseededTarget(target string) error {
	// Same as discardUnseededTarget, then the bare directory is put back; the
	// registration failure is reported only after that (see below).
	gitCommonDir, _ := workspaceGitCommonDir(target)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("workspace: reset unseeded workspace %s: %w — a process still holds a file in it "+
			"(OneDrive, an antivirus, a process the hook left running): close it, or delete the folder by hand", target, err)
	}
	// The registration goes while the path is absent (`git worktree remove`
	// deletes a path that exists). Its failure must not leave the directory
	// missing under an active marker — that state refuses every later attempt —
	// so the directory is recreated first and the failure reported after.
	var regErr error
	if gitCommonDir != "" {
		regErr = removeGitWorktreeRegistration(gitCommonDir, target)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		return errors.Join(fmt.Errorf("workspace: recreate unseeded workspace %s: %w", target, err), regErr)
	}
	if regErr != nil {
		return fmt.Errorf("workspace: reset unseeded workspace %s: %w (run `git worktree prune` in the host repository)", target, regErr)
	}
	return nil
}

func (w *Workspaces) createForRun(issueID, runID string, trackSeed bool) (path string, created, reseeded bool, err error) {
	if issueID == "" {
		return "", false, false, errors.New("workspace: issue id required")
	}
	defer w.lockGeneration(issueID, runID)()

	if err := os.MkdirAll(w.root, 0o755); err != nil {
		return "", false, false, fmt.Errorf("workspace: mkdir root: %w", err)
	}
	rootCanon, err := filepath.EvalSymlinks(w.root)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: canonicalize root: %w", err)
	}

	namespace := filepath.Join(w.root, workspaceKeyNamespace)
	if err := os.Mkdir(namespace, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", false, false, fmt.Errorf("workspace: mkdir key namespace: %w", err)
	}
	namespaceInfo, err := os.Lstat(namespace)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: stat key namespace: %w", err)
	}
	if !namespaceInfo.IsDir() || namespaceInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, false, fmt.Errorf("workspace: key namespace %s is not a real directory", namespace)
	}
	namespaceCanon, err := filepath.EvalSymlinks(namespace)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: canonicalize key namespace: %w", err)
	}
	if !isWithin(namespaceCanon, rootCanon) {
		return "", false, false, fmt.Errorf("workspace: key namespace %q escapes root %q", namespaceCanon, rootCanon)
	}

	ownersDir := filepath.Join(namespace, workspaceOwnersDir)
	if err := os.Mkdir(ownersDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", false, false, fmt.Errorf("workspace: mkdir ownership namespace: %w", err)
	}
	ownersInfo, err := os.Lstat(ownersDir)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: stat ownership namespace: %w", err)
	}
	if !ownersInfo.IsDir() || ownersInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, false, fmt.Errorf("workspace: ownership namespace %s is not a real directory", ownersDir)
	}
	ownersCanon, err := filepath.EvalSymlinks(ownersDir)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: canonicalize ownership namespace: %w", err)
	}
	if !isWithin(ownersCanon, rootCanon) {
		return "", false, false, fmt.Errorf("workspace: ownership namespace %q escapes root %q", ownersCanon, rootCanon)
	}

	// An unseeded generation left under the long name by an earlier build is
	// removed first, so the paths below resolve to the short name and the seeding
	// step starts afresh in it.
	discardedLegacy, err := w.discardUnseededLegacy(issueID, runID, rootCanon)
	if err != nil {
		return "", false, false, err
	}
	reseeded = discardedLegacy

	target := w.PathForRun(issueID, runID)
	ownerPath := w.ownerPathForRun(issueID, runID)
	targetInfo, targetErr := os.Lstat(target)
	owner, ownerErr := readWorkspaceOwner(ownerPath)
	resetUnseeded := false
	if errors.Is(targetErr, os.ErrNotExist) && ownerErr == nil && runID == "" {
		// The stable API can be retried after a crash between deleting a
		// retired target and deleting its marker. Active orphan markers remain
		// fail-closed: only an explicit retirement proves deletion was allowed.
		if verifyErr := verifyWorkspaceOwner(owner, issueID, runID, workspaceOwnerRetired); verifyErr == nil {
			if err := os.Remove(ownerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", false, false, fmt.Errorf("workspace: clear retired ownership marker %s: %w", ownerPath, err)
			}
			ownerErr = os.ErrNotExist
		}
	}

	switch {
	case targetErr == nil:
		if ownerErr != nil {
			return "", false, false, fmt.Errorf(
				"workspace: refusing to adopt existing target %s without valid ownership marker %s: %w; "+
					"manual recovery: inspect the unowned target and remove or relocate it before retrying",
				target, ownerPath, ownerErr,
			)
		}
		if err := verifyWorkspaceOwner(owner, issueID, runID, workspaceOwnerActive); err != nil {
			return "", false, false, fmt.Errorf("workspace: verify ownership marker %s: %w", ownerPath, err)
		}
		// The directory is ours and was never declared seeded: the leftover of
		// an after_create that failed or was interrupted. Reset below, once the
		// target is known to be a real directory under the root.
		resetUnseeded = trackSeed && owner.SeedPending
	case !errors.Is(targetErr, os.ErrNotExist):
		return "", false, false, fmt.Errorf("workspace: stat target: %w", targetErr)
	case ownerErr == nil:
		return "", false, false, fmt.Errorf(
			"workspace: owned target %s is missing while marker %s is %q; inspect or remove that marker for manual recovery",
			target, ownerPath, owner.State,
		)
	case !errors.Is(ownerErr, os.ErrNotExist):
		return "", false, false, fmt.Errorf("workspace: read ownership marker %s: %w", ownerPath, ownerErr)
	default:
		if err := os.Mkdir(target, 0o755); err != nil {
			return "", false, false, fmt.Errorf("workspace: mkdir target: %w", err)
		}
		// os.Mkdir is the creation authority. Unlike a Stat+MkdirAll
		// sequence, exactly one concurrent caller can observe success and
		// therefore report created=true/run the after_create hook.
		created = true
		record := workspaceOwnerRecord{
			FormatVersion: 1,
			IssueID:       issueID,
			RunID:         runID,
			State:         workspaceOwnerActive,
			SeedPending:   trackSeed,
			SeedTracked:   trackSeed,
		}
		if err := createWorkspaceOwner(ownerPath, record); err != nil {
			// Never remove the target here: even in this narrow failure
			// window another process may already have written into it.
			return "", false, false, fmt.Errorf(
				"workspace: publish ownership marker %s (target preserved unowned at %s for manual recovery): %w",
				ownerPath, target, err,
			)
		}
		targetInfo, targetErr = os.Lstat(target)
		if targetErr != nil {
			return "", false, false, fmt.Errorf("workspace: stat freshly-created target: %w", targetErr)
		}
	}

	if !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, false, fmt.Errorf("workspace: %s exists and is not a real directory", target)
	}
	canon, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", false, false, fmt.Errorf("workspace: canonicalize target: %w", err)
	}
	if !isWithin(canon, rootCanon) {
		return "", false, false, fmt.Errorf("workspace: target %q escapes root %q (symlink or traversal)", canon, rootCanon)
	}
	if resetUnseeded {
		if err := resetUnseededTarget(canon); err != nil {
			return "", false, false, err
		}
		created, reseeded = true, true
	}
	return canon, created, reseeded, nil
}

// RetireForRun revokes the exact issue/run generation before teardown. Once
// retired, CreateForRun refuses the generation even if a late writer recreates
// its old absolute path.
func (w *Workspaces) RetireForRun(issueID, runID string) error {
	if issueID == "" {
		return errors.New("workspace: issue id required")
	}
	defer w.lockGeneration(issueID, runID)()

	// An unseeded legacy generation is removed outright: it has no short-named
	// marker to retire, and nothing in it is worth a retirement.
	if discarded, err := w.discardUnseededLegacyForTeardown(issueID, runID); err != nil || discarded {
		return err
	}

	ownerPath := w.ownerPathForRun(issueID, runID)
	owner, err := readWorkspaceOwner(ownerPath)
	if err != nil {
		return fmt.Errorf("workspace: read ownership marker %s for retirement: %w", ownerPath, err)
	}
	if err := verifyWorkspaceOwnerID(owner, issueID, runID); err != nil {
		return fmt.Errorf("workspace: verify ownership marker %s for retirement: %w", ownerPath, err)
	}
	if owner.State == workspaceOwnerRetired {
		return nil
	}
	if owner.State != workspaceOwnerActive {
		return fmt.Errorf("workspace: unsupported ownership state %q", owner.State)
	}
	if _, err := w.verifyOwnedTarget(issueID, runID); err != nil {
		return err
	}
	owner.State = workspaceOwnerRetired
	if err := replaceWorkspaceOwner(ownerPath, owner); err != nil {
		return fmt.Errorf("workspace: retire ownership marker %s: %w", ownerPath, err)
	}
	return nil
}

// Remove preserves the pre-generational API while applying the same safe
// active→retired→deleted lifecycle to the stable workspace. It is idempotent
// when both the stable target and its ownership marker are already absent.
func (w *Workspaces) Remove(issueID string) error {
	retireErr := w.RetireForRun(issueID, "")
	if retireErr == nil {
		return w.RemoveForRun(issueID, "")
	}
	removeErr := w.RemoveForRun(issueID, "")
	if removeErr == nil {
		return nil
	}
	return errors.Join(retireErr, removeErr)
}

// RemoveForRun deletes an explicitly retired generation. If teardown was
// interrupted after directory removal, a later RemoveForRun call safely clears
// the retired marker. CreateForRun deliberately does not clear a run-specific
// tombstone: callers must finish or retry RemoveForRun before reusing that exact
// issue/run generation, preserving isolation from late writers. An active marker
// with a missing target remains fail-closed because its disappearance was not
// authorized by this lifecycle.
func (w *Workspaces) RemoveForRun(issueID, runID string) error {
	if issueID == "" {
		return errors.New("workspace: issue id required")
	}
	defer w.lockGeneration(issueID, runID)()

	// keyFor sends an unseeded legacy generation to the short name, where nothing
	// exists: clear its long-named leftover here, or it stays on disk for good.
	if _, err := w.discardUnseededLegacyForTeardown(issueID, runID); err != nil {
		return err
	}

	target := w.PathForRun(issueID, runID)
	ownerPath := w.ownerPathForRun(issueID, runID)
	owner, ownerErr := readWorkspaceOwner(ownerPath)
	if errors.Is(ownerErr, os.ErrNotExist) {
		if _, targetErr := os.Lstat(target); errors.Is(targetErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("workspace: refusing removal without ownership marker %s", ownerPath)
	}
	if ownerErr != nil {
		return fmt.Errorf("workspace: refusing removal with invalid ownership marker %s: %w", ownerPath, ownerErr)
	}
	if err := verifyWorkspaceOwnerID(owner, issueID, runID); err != nil {
		return fmt.Errorf("workspace: verify ownership marker %s: %w", ownerPath, err)
	}

	rootCanon, err := filepath.EvalSymlinks(w.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("workspace: canonicalize root: %w", err)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if owner.State != workspaceOwnerRetired {
			return fmt.Errorf(
				"workspace: target %s is absent but ownership marker %s is %q; inspect or remove that marker for manual recovery",
				target, ownerPath, owner.State,
			)
		}
		if err := os.Remove(ownerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("workspace: remove retired ownership marker %s: %w", ownerPath, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("workspace: stat target: %w", err)
	}
	if err := verifyWorkspaceOwner(owner, issueID, runID, workspaceOwnerRetired); err != nil {
		return fmt.Errorf("workspace: verify retired ownership marker %s: %w", ownerPath, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace: refusing to remove non-directory target %q", target)
	}
	canon, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("workspace: canonicalize target: %w", err)
	}
	if rootCanon != "" && !isWithin(canon, rootCanon) {
		return fmt.Errorf("workspace: refusing to remove %q outside root", canon)
	}
	if err := os.RemoveAll(canon); err != nil {
		return fmt.Errorf("workspace: remove retired target %s: %w", canon, err)
	}
	if err := os.Remove(ownerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(
			"workspace: target removed but retired ownership marker %s remains; remove it manually or retry cleanup: %w",
			ownerPath, err,
		)
	}
	return nil
}

func (w *Workspaces) ownerPathForRun(issueID, runID string) string {
	return filepath.Join(w.root, workspaceKeyNamespace, workspaceOwnersDir, w.keyFor(issueID, runID)+".json")
}

// keyFor returns the directory name of one generation: the short key, unless
// the generation already exists under the legacy long key (its directory or its
// ownership marker is there and nothing is under the short key). That keeps a
// workspace made by an earlier build, with its seed_pending / seed_tracked
// marker, found and owned instead of orphaned beside a second one. A new
// generation always gets the short key.
//
// The answer depends only on what is on disk, never on a lock, so the
// lock-free probes (resumeGenerationState, buildRunningEntry) and the locked
// paths agree. A generation is deleted target first, marker second, and either
// one alone keeps resolving to the key it was made under.
func (w *Workspaces) keyFor(issueID, runID string) string {
	short, legacy := workspaceKeys(issueID, runID)
	namespace := filepath.Join(w.root, workspaceKeyNamespace)
	if keyPresent(namespace, short) || !keyPresent(namespace, legacy) {
		return short
	}
	if w.legacyIsUnseeded(namespace, legacy, issueID, runID) {
		return short
	}
	return legacy
}

// legacyIsUnseeded reports whether a legacy-named generation never got its
// seeding done, so nothing in it is worth keeping under its long name: its
// marker says the seed is pending (the directory may be missing, half-built or
// empty), or, when an after_create hook is configured, it is an empty directory
// under a marker from a build that did not track seeding (the bars-project case:
// `git worktree add` failed on the long name and left the bare directory).
// Re-seeding such a generation under the long name would fail the same way, so
// keyFor sends it to the short name and discardUnseededLegacy removes what it
// left. Without a hook an empty directory is not a failure (a `worktree: auto`
// run works in its own checkout), and the generation stays resumable under the
// legacy name. A seeded workspace, a retired one and a marker whose directory
// vanished are none of this: they stay under the legacy name, and the
// lifecycle's fail-closed rules apply to them as before.
func (w *Workspaces) legacyIsUnseeded(namespace, legacy, issueID, runID string) bool {
	owner, err := readWorkspaceOwner(filepath.Join(namespace, workspaceOwnersDir, legacy+".json"))
	if err != nil || verifyWorkspaceOwner(owner, issueID, runID, workspaceOwnerActive) != nil {
		return false
	}
	dir := filepath.Join(namespace, legacy)
	info, err := os.Lstat(dir)
	switch {
	case err == nil:
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		return owner.SeedPending || (!owner.SeedTracked && w.seedHookConfigured() && dirIsEmpty(dir))
	case errors.Is(err, os.ErrNotExist):
		// A discard interrupted between removing the directory and the marker.
		return owner.SeedPending
	default:
		return false
	}
}

// discardUnseededLegacy removes the leftover of a legacy-named generation that
// legacyIsUnseeded sent to the short name, so a fresh, short-named workspace is
// created in its place. It reports whether it removed one. The marker is first
// made seed-pending (a marker without the mark could not be told apart from a
// seeded one once its directory is gone), then the directory goes, then the
// marker: an interruption at any point is retried by the next call. The caller
// holds the generation lock.
func (w *Workspaces) discardUnseededLegacy(issueID, runID, rootCanon string) (bool, error) {
	short, legacy := workspaceKeys(issueID, runID)
	namespace := filepath.Join(w.root, workspaceKeyNamespace)
	if keyPresent(namespace, short) || !keyPresent(namespace, legacy) ||
		!w.legacyIsUnseeded(namespace, legacy, issueID, runID) {
		return false, nil
	}
	dir := filepath.Join(namespace, legacy)
	if canon, err := filepath.EvalSymlinks(dir); err == nil && !isWithin(canon, rootCanon) {
		// A junction or reparse point that legacyIsUnseeded's symlink test does
		// not catch: never delete through it.
		return false, fmt.Errorf("workspace: legacy workspace %s resolves to %q outside root %q — refusing to discard it", dir, canon, rootCanon)
	}
	markerPath := filepath.Join(namespace, workspaceOwnersDir, legacy+".json")
	owner, err := readWorkspaceOwner(markerPath)
	if err != nil {
		return false, fmt.Errorf("workspace: read ownership marker %s: %w", markerPath, err)
	}
	if !owner.SeedPending {
		owner.SeedPending, owner.SeedTracked = true, true
		if err := replaceWorkspaceOwner(markerPath, owner); err != nil {
			return false, fmt.Errorf("workspace: mark %s seed-pending: %w", markerPath, err)
		}
	}
	if err := discardUnseededTarget(dir); err != nil {
		return false, err
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("workspace: remove ownership marker %s of a discarded workspace: %w", markerPath, err)
	}
	return true, nil
}

// discardUnseededLegacyForTeardown is discardUnseededLegacy for the retire and
// remove paths, which have no root canonicalization of their own. A root that
// does not exist holds nothing to discard. The caller holds w.mu.
func (w *Workspaces) discardUnseededLegacyForTeardown(issueID, runID string) (bool, error) {
	rootCanon, err := filepath.EvalSymlinks(w.root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("workspace: canonicalize root: %w", err)
	}
	return w.discardUnseededLegacy(issueID, runID, rootCanon)
}

// keyPresent reports whether a generation's directory or ownership marker
// exists under namespace. A path that cannot be examined counts as present: the
// caller then keeps using that key and meets the same error where it acts on it.
func keyPresent(namespace, key string) bool {
	for _, path := range []string{
		filepath.Join(namespace, key),
		filepath.Join(namespace, workspaceOwnersDir, key+".json"),
	} {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

// resumeGeneration returns the active, verified workspace shape that owns a
// resumable run. It preserves the historical compact result for lifecycle
// callers; resumeGenerationState exposes the separate exists bit needed by
// resolveRunID to distinguish a missing workspace (safe fresh restart) from an
// existing but unowned/corrupt shape (fail closed).
func (w *Workspaces) resumeGeneration(issueID, runID string) (string, bool, error) {
	generation, _, managed, err := w.resumeGenerationState(issueID, runID)
	return generation, managed, err
}

// resumeGenerationState probes the run-scoped shape first, then the stable
// workspace retained from an older keep-mode dispatch. If the unique run shape
// exists but is invalid, it wins and we do not fall through to an unrelated
// stable workspace. The probes are deliberately lock-free: they run on the
// dispatcher actor and must never wait behind a worker's recursive delete.
func (w *Workspaces) resumeGenerationState(issueID, runID string) (generation string, exists, managed bool, err error) {
	exists, err = w.generationShapeExists(issueID, runID)
	if err != nil {
		return runID, false, false, err
	}
	if exists {
		managed, err = w.generationIsManaged(issueID, runID)
		return runID, true, managed, err
	}
	exists, err = w.generationShapeExists(issueID, "")
	if err != nil {
		return "", false, false, err
	}
	if exists {
		managed, err = w.generationIsManaged(issueID, "")
		return "", true, managed, err
	}
	return "", false, false, nil
}

func (w *Workspaces) generationShapeExists(issueID, runID string) (bool, error) {
	for _, path := range []string{
		w.PathForRun(issueID, runID),
		w.ownerPathForRun(issueID, runID),
	} {
		if _, err := os.Lstat(path); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("workspace: probe generation path %s: %w", path, err)
		}
	}
	return false, nil
}

func (w *Workspaces) generationIsManaged(issueID, runID string) (bool, error) {
	ownerPath := w.ownerPathForRun(issueID, runID)
	data, err := os.ReadFile(ownerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("workspace: read ownership marker %s during generation probe: %w", ownerPath, err)
	}
	var owner workspaceOwnerRecord
	if err := json.Unmarshal(data, &owner); err != nil {
		// A malformed marker is an invalid v2 shape, not an operational
		// filesystem failure. Reject the resume so the caller can isolate a
		// fresh generation while preserving the corrupt shape for recovery.
		return false, nil
	}
	if err := verifyWorkspaceOwner(owner, issueID, runID, workspaceOwnerActive); err != nil {
		return false, nil
	}

	target := w.PathForRun(issueID, runID)
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("workspace: stat owned target during generation probe: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	canon, err := filepath.EvalSymlinks(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("workspace: canonicalize owned target during generation probe: %w", err)
	}
	rootCanon, err := filepath.EvalSymlinks(w.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("workspace: canonicalize root during generation probe: %w", err)
	}
	if !isWithin(canon, rootCanon) {
		return false, nil
	}
	return true, nil
}

func (w *Workspaces) verifyOwnedTarget(issueID, runID string) (string, error) {
	target := w.PathForRun(issueID, runID)
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("workspace: stat owned target: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace: owned target is not a real directory: %s", target)
	}
	canon, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("workspace: canonicalize owned target: %w", err)
	}
	rootCanon, err := filepath.EvalSymlinks(w.root)
	if err != nil {
		return "", fmt.Errorf("workspace: canonicalize root: %w", err)
	}
	if !isWithin(canon, rootCanon) {
		return "", fmt.Errorf("workspace: owned target %q escapes root %q", canon, rootCanon)
	}
	return canon, nil
}

func readWorkspaceOwner(path string) (workspaceOwnerRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceOwnerRecord{}, err
	}
	var owner workspaceOwnerRecord
	if err := json.Unmarshal(data, &owner); err != nil {
		return workspaceOwnerRecord{}, err
	}
	return owner, nil
}

func verifyWorkspaceOwnerID(owner workspaceOwnerRecord, issueID, runID string) error {
	if owner.FormatVersion != 1 {
		return fmt.Errorf("workspace: unsupported ownership marker version %d", owner.FormatVersion)
	}
	if owner.IssueID != issueID {
		return fmt.Errorf("workspace: ownership marker belongs to %q, not %q", owner.IssueID, issueID)
	}
	if owner.RunID != runID {
		return fmt.Errorf("workspace: ownership marker run is %q, not %q", owner.RunID, runID)
	}
	return nil
}

func verifyWorkspaceOwner(owner workspaceOwnerRecord, issueID, runID string, state workspaceOwnerState) error {
	if err := verifyWorkspaceOwnerID(owner, issueID, runID); err != nil {
		return err
	}
	if owner.State != state {
		return fmt.Errorf(
			"workspace: ownership marker for %q is %q, expected %q",
			issueID, owner.State, state,
		)
	}
	return nil
}

func createWorkspaceOwner(path string, owner workspaceOwnerRecord) error {
	return createWorkspaceOwnerWithOpener(path, owner, func(name string, flag int, perm os.FileMode) (workspaceOwnerFile, error) {
		return os.OpenFile(name, flag, perm)
	})
}

type workspaceOwnerFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type workspaceOwnerFileOpener func(name string, flag int, perm os.FileMode) (workspaceOwnerFile, error)

func createWorkspaceOwnerWithOpener(path string, owner workspaceOwnerRecord, open workspaceOwnerFileOpener) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	f, err := open(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	publishErr := error(nil)
	if _, err := f.Write(append(data, '\n')); err != nil {
		publishErr = err
	} else if err := f.Sync(); err != nil {
		publishErr = err
	}
	if err := f.Close(); publishErr == nil {
		publishErr = err
	}
	if publishErr == nil {
		return nil
	}

	// OpenFile(O_EXCL) proves this invocation created the marker. If
	// publication fails, remove that marker so callers never mistake
	// truncated JSON (or a close whose durability is unknown) for a valid
	// ownership record. The workspace directory is deliberately untouched:
	// another process may already have written recoverable output into it.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(publishErr, fmt.Errorf("workspace: remove failed ownership marker: %w", err))
	}
	return publishErr
}

func replaceWorkspaceOwner(path string, owner workspaceOwnerRecord) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".owner-atomic-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameReplace(tmpPath, path)
}

var sanitizeKeyRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// workspaceKeys returns the current (short) and the legacy (long) directory
// name of one generation. An empty runID is the stable per-issue workspace.
func workspaceKeys(issueID, runID string) (short, legacy string) {
	identity := issueID
	if runID != "" {
		identity = workspaceRunIdentity(issueID, runID)
	}
	return workspaceKeyWithIdentity(issueID, identity), legacyWorkspaceKeyWithIdentity(issueID, identity)
}

// workspaceRunIdentity derives the identity of a run generation from both the
// original issue ID and the unique run ID.
func workspaceRunIdentity(issueID, runID string) string {
	return "iterion-dispatch-workspace-run-v1\x00" + issueID + "\x00" + runID
}

// workspaceKeyWithIdentity combines a bounded human-readable prefix with a
// truncated digest of the identity (see workspaceKeySlugMax).
func workspaceKeyWithIdentity(issueID, identity string) string {
	return boundedSlug(issueID, workspaceKeySlugMax) + "--" + identityDigest(identity)[:workspaceKeyDigestHex]
}

// legacyWorkspaceKeyWithIdentity is the first v2 key format: a longer slug and
// the complete digest.
func legacyWorkspaceKeyWithIdentity(issueID, identity string) string {
	return boundedSlug(issueID, legacyWorkspaceKeySlugMax) + "--" + identityDigest(identity)
}

func boundedSlug(issueID string, max int) string {
	slug := sanitizeKey(issueID)
	if len(slug) > max {
		// sanitizeKey emits ASCII only, so a byte slice cannot split UTF-8.
		slug = slug[:max]
	}
	return slug
}

func identityDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x", sum)
}

// sanitizeKey replaces filesystem-hostile characters with underscore.
// A leading dot is also escaped so the directory is not hidden.
func sanitizeKey(s string) string {
	out := sanitizeKeyRe.ReplaceAllString(s, "_")
	out = strings.TrimSpace(out)
	if out == "" {
		out = "_"
	}
	if strings.HasPrefix(out, ".") {
		out = "_" + out
	}
	return out
}

// isWithin reports whether child sits at or below parent. Both must be
// absolute, canonical paths.
func isWithin(child, parent string) bool {
	if child == parent {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(child, prefix)
}
