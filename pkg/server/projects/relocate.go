package projects

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gofrs/flock"

	"github.com/arketov/iterion/pkg/store"
)

// A project's store is addressed by the path of its folder, so moving the
// folder leaves the board, the dispatcher settings and the runs in a slot the
// new path no longer points at. The registry still holds the exact pair "old
// folder <-> old slot" (a pinned StoreDir survives pruning), which is what
// makes an offer to carry the data over safe: nothing is matched by guessing.

// workspaceLockName is the lease the workspace host holds on a slot while a
// runtime owns it (pkg/server/workspace.go).
const workspaceLockName = ".workspace-owner.lock"

var (
	// ErrDestinationNotEmpty is returned when the target slot already holds a
	// board, a dispatcher config or runs: nothing is ever merged or overwritten.
	ErrDestinationNotEmpty = errors.New("relocate: the destination store already holds data")
	// ErrSourceBusy is returned when a live process owns the source slot.
	ErrSourceBusy = errors.New("relocate: the source store is in use by another process")
	// ErrNoSource is returned when the source slot is missing or not a folder.
	ErrNoSource = errors.New("relocate: the source store was not found")
)

// RelocationSource is a registered project whose folder is gone but whose
// store still holds data that could belong to a moved project.
type RelocationSource struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Dir                 string `json:"dir"`
	StoreDir            string `json:"store_dir"`
	Cards               int    `json:"cards"`
	Runs                int    `json:"runs"`
	HasDispatcherConfig bool   `json:"has_dispatcher_config"`
}

// RelocationSources lists the registry entries whose data target may inherit:
// a pinned store that exists, an old folder that no longer exists, the same
// folder name as the target, data worth carrying, and a store of its own. The
// folder name is a necessary condition, not proof, which is why the studio
// asks the owner before anything is copied.
func (c *Config) RelocationSources(target Project) []RelocationSource {
	var out []RelocationSource
	targetStore := canonicalExistingOrClean(target.StoreDir)
	for _, p := range c.RecentProjects {
		if p.ID == target.ID || p.StoreDir == "" || p.Dir == "" {
			continue
		}
		if _, err := os.Stat(p.Dir); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if !sameName(filepath.Base(p.Dir), target.Name) {
			continue
		}
		if sameName(canonicalExistingOrClean(p.StoreDir), targetStore) {
			continue
		}
		if info, err := os.Stat(p.StoreDir); err != nil || !info.IsDir() {
			continue
		}
		src := RelocationSource{ID: p.ID, Name: p.Name, Dir: p.Dir, StoreDir: p.StoreDir}
		src.Cards = countFiles(filepath.Join(p.StoreDir, "dispatcher", "issues"), ".json")
		src.Runs = countDirs(filepath.Join(p.StoreDir, "runs"))
		src.HasDispatcherConfig = exists(filepath.Join(p.StoreDir, "dispatcher", "dispatcher.json"))
		if src.Cards == 0 && src.Runs == 0 && !src.HasDispatcherConfig {
			continue
		}
		out = append(out, src)
	}
	return out
}

// SlotIsEmpty reports whether storeDir holds no data worth protecting: no
// saved dispatcher config, no cards, no events and no runs. The board.json and
// the empty issues/ folder the board creates on first open are not data.
func SlotIsEmpty(storeDir string) (bool, error) {
	if exists(filepath.Join(storeDir, "dispatcher", "dispatcher.json")) {
		return false, nil
	}
	if countFiles(filepath.Join(storeDir, "dispatcher", "issues"), ".json") > 0 {
		return false, nil
	}
	if info, err := os.Stat(filepath.Join(storeDir, "dispatcher", "events.jsonl")); err == nil && info.Size() > 0 {
		return false, nil
	}
	entries, err := os.ReadDir(filepath.Join(storeDir, "runs"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return len(entries) == 0, nil
}

// Relocate copies the board, the dispatcher settings and the run history of
// src's store into dst's empty store. The source is never modified. What stays
// behind on purpose: secrets, skills, marketplace and worktrees (a worktree
// points at the old repository), and runtime.json — dst gets a "stopped"
// intent instead, because a dispatcher with a config and no saved intent
// starts itself on the next boot. Absolute paths under src.Dir inside the
// dispatcher config are rewritten to dst.Dir.
func Relocate(src, dst Project) error {
	return relocate(src, dst, copyFileContents)
}

func relocate(src, dst Project, copyFn func(from, to string) error) (err error) {
	if info, statErr := os.Stat(src.StoreDir); src.StoreDir == "" || statErr != nil || !info.IsDir() {
		return ErrNoSource
	}
	if dst.StoreDir == "" {
		return errors.New("relocate: the destination project has no store")
	}
	if sameName(canonicalExistingOrClean(src.StoreDir), canonicalExistingOrClean(dst.StoreDir)) {
		return errors.New("relocate: the source and the destination are the same store")
	}
	unlock, err := lockSource(src.StoreDir)
	if err != nil {
		return err
	}
	defer unlock()
	empty, err := SlotIsEmpty(dst.StoreDir)
	if err != nil {
		return fmt.Errorf("relocate: inspect destination: %w", err)
	}
	if !empty {
		return ErrDestinationNotEmpty
	}

	// Everything is staged inside the destination slot (same volume, so the
	// final renames are cheap and atomic per entry) and only moved into place
	// once it is complete: a failure leaves the slot as it was.
	staging := filepath.Join(dst.StoreDir, ".relocate-"+randomID())
	defer func() { _ = os.RemoveAll(staging) }()
	if err := stageRelocation(src, dst, staging, copyFn); err != nil {
		return err
	}
	return placeRelocation(staging, dst.StoreDir)
}

func lockSource(storeDir string) (func(), error) {
	lockPath := filepath.Join(storeDir, workspaceLockName)
	// A slot nobody ever owned has no lock file; do not create one in a source
	// that must stay untouched.
	if !exists(lockPath) {
		return func() {}, nil
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("relocate: lock the source store: %w", err)
	}
	if !locked {
		return nil, ErrSourceBusy
	}
	return func() { _ = lock.Unlock() }, nil
}

func stageRelocation(src, dst Project, staging string, copyFn func(from, to string) error) error {
	srcDisp := filepath.Join(src.StoreDir, "dispatcher")
	stageDisp := filepath.Join(staging, "dispatcher")
	if err := os.MkdirAll(stageDisp, 0o755); err != nil {
		return fmt.Errorf("relocate: create staging: %w", err)
	}
	if err := stageDispatcherConfig(filepath.Join(srcDisp, "dispatcher.json"), filepath.Join(stageDisp, "dispatcher.json"), src.Dir, dst.Dir); err != nil {
		return err
	}
	for _, name := range []string{"board.json", "events.jsonl"} {
		if !exists(filepath.Join(srcDisp, name)) {
			continue
		}
		if err := copyFn(filepath.Join(srcDisp, name), filepath.Join(stageDisp, name)); err != nil {
			return fmt.Errorf("relocate: copy %s: %w", name, err)
		}
	}
	if err := copyTree(filepath.Join(srcDisp, "issues"), filepath.Join(stageDisp, "issues"), copyFn); err != nil {
		return fmt.Errorf("relocate: copy issues: %w", err)
	}
	if err := copyTree(filepath.Join(src.StoreDir, "runs"), filepath.Join(staging, "runs"), copyFn); err != nil {
		return fmt.Errorf("relocate: copy runs: %w", err)
	}
	// The same shape pkg/dispatcher persists (runtimeStateFile); a test there
	// loads this very file through NewManager.
	stopped, err := json.MarshalIndent(map[string]string{"desired": "stopped"}, "", "  ")
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(filepath.Join(stageDisp, "runtime.json"), append(stopped, '\n'), 0o644); err != nil {
		return fmt.Errorf("relocate: write runtime state: %w", err)
	}
	return nil
}

// placeRelocation moves the staged entries into the slot. The only things it
// removes are the empty defaults SlotIsEmpty vouched for; on a failure the
// entries it already placed are taken back out.
func placeRelocation(staging, storeDir string) (err error) {
	var placed []string
	defer func() {
		if err != nil {
			for _, p := range placed {
				_ = os.RemoveAll(p)
			}
		}
	}()
	move := func(from, to string) error {
		if _, statErr := os.Lstat(to); statErr == nil {
			if err := os.Remove(to); err != nil {
				return fmt.Errorf("relocate: clear default %s: %w", to, err)
			}
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("relocate: place %s: %w", to, err)
		}
		placed = append(placed, to)
		return nil
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "dispatcher"), 0o755); err != nil {
		return fmt.Errorf("relocate: create dispatcher folder: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(staging, "dispatcher"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := move(filepath.Join(staging, "dispatcher", e.Name()), filepath.Join(storeDir, "dispatcher", e.Name())); err != nil {
			return err
		}
	}
	if exists(filepath.Join(staging, "runs")) {
		return move(filepath.Join(staging, "runs"), filepath.Join(storeDir, "runs"))
	}
	return nil
}

// stageDispatcherConfig copies dispatcher.json with every string value that is
// an absolute path under oldRoot rewritten to newRoot. Relative paths and $ENV
// references are left as written. A file with nothing to rewrite is copied
// byte for byte.
func stageDispatcherConfig(from, to, oldRoot, newRoot string) error {
	data, err := os.ReadFile(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("relocate: read dispatcher.json: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("relocate: parse dispatcher.json: %w", err)
	}
	doc, changed := rewriteValue(doc, oldRoot, newRoot)
	if changed {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return fmt.Errorf("relocate: encode dispatcher.json: %w", err)
		}
		data = buf.Bytes()
	}
	if err := store.WriteFileAtomic(to, data, 0o600); err != nil {
		return fmt.Errorf("relocate: write dispatcher.json: %w", err)
	}
	return nil
}

func rewriteValue(v any, oldRoot, newRoot string) (any, bool) {
	switch t := v.(type) {
	case string:
		if s, ok := rewriteRoot(t, oldRoot, newRoot); ok {
			return s, true
		}
	case []any:
		changed := false
		for i := range t {
			var c bool
			t[i], c = rewriteValue(t[i], oldRoot, newRoot)
			changed = changed || c
		}
		return t, changed
	case map[string]any:
		changed := false
		for k := range t {
			var c bool
			t[k], c = rewriteValue(t[k], oldRoot, newRoot)
			changed = changed || c
		}
		return t, changed
	}
	return v, false
}

// rewriteRoot replaces a leading oldRoot in s with newRoot when s is oldRoot
// itself or a path below it. The match stops at a separator, so a folder that
// merely shares the prefix (`proj2`) is not touched. On Windows the match
// ignores case and the direction of the slashes.
func rewriteRoot(s, oldRoot, newRoot string) (string, bool) {
	oldRoot, newRoot = trimSeps(oldRoot), trimSeps(newRoot)
	if oldRoot == "" || len(s) < len(oldRoot) {
		return "", false
	}
	if !samePathText(s[:len(oldRoot)], oldRoot) {
		return "", false
	}
	rest := s[len(oldRoot):]
	if rest != "" && !isSep(rest[0]) {
		return "", false
	}
	return newRoot + rest, true
}

func isSep(c byte) bool {
	return c == '/' || (runtime.GOOS == "windows" && c == '\\')
}

func trimSeps(s string) string {
	if runtime.GOOS == "windows" {
		return strings.TrimRight(s, `/\`)
	}
	return strings.TrimRight(s, "/")
}

func samePathText(a, b string) bool {
	if runtime.GOOS != "windows" {
		return a == b
	}
	return strings.EqualFold(strings.ReplaceAll(a, `\`, "/"), strings.ReplaceAll(b, `\`, "/"))
}

// sameName compares folder names and paths the way the host file system does.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// copyTree copies the regular files under from into to. Symlinks and lock
// files are skipped; a missing from is not an error.
func copyTree(from, to string, copyFn func(from, to string) error) error {
	if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".lock"):
			return nil
		}
		return copyFn(path, target)
	})
}

func copyFileContents(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func countFiles(dir, suffix string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}

func countDirs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}
