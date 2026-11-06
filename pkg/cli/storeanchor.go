package cli

import (
	"os"
	"path/filepath"

	"github.com/arketov/iterion/pkg/git"
	"github.com/arketov/iterion/pkg/store"
)

// storeAnchorDir returns the directory a command should hand to
// store.ResolveStoreDir.
//
// The working directory wins. A .bot that lives inside the project it drives
// is the common case, and anchoring on the bot's own directory keyed the store
// on that subdirectory: `iterion run project/bots/x/main.bot` wrote to
// ~/.iterion/projects/<project-bots-x-key>/ while resume, inspect, issue,
// dispatch and the studio all resolved <project>/.iterion. Launching worked
// and every follow-up reported "run not found".
//
// The bot's directory stays as the fallback for the case the working
// directory is genuinely unavailable, and for an empty iterFile the caller
// still gets a usable anchor.
func storeAnchorDir(iterFile string) string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	if iterFile == "" {
		return ""
	}
	return filepath.Dir(iterFile)
}

// runStoreDir resolves the store a run is launched into, resumed from, and
// gated against. Every one of those three must agree: when the schedule gate
// listed one store while the tick wrote to another, overlap protection silently
// stopped working. Going through one function makes that structural instead of
// three call sites that happen to match.
func runStoreDir(iterFile, override string) string {
	return store.ResolveStoreDir(storeAnchorDir(iterFile), override)
}

// legacyRunStoreDir returns the pre-cwd-anchoring store that holds runID, or ""
// when there is nothing to fall back to.
//
// Runs launched before the anchor moved live under the store keyed on the
// .bot's directory. Resume must keep finding them, otherwise upgrading orphans
// every paused / failed_resumable run in flight and the operator gets exactly
// the "run not found" this change set out to remove. Only consulted when the
// caller passed no explicit --store-dir and the current store does not hold the
// run.
func legacyRunStoreDir(iterFile, override, resolved, runID string) string {
	if override != "" || iterFile == "" || runID == "" {
		return ""
	}
	legacy := store.ResolveStoreDir(filepath.Dir(iterFile), "")
	if legacy == resolved || !runStoreHas(legacy, runID) {
		return ""
	}
	return legacy
}

// ancestorStoreHint returns the store of a parent folder the run missed, or ""
// when there is none to point at.
//
// The store is keyed on the anchor directory, so `iterion run` from
// project/bots/x writes to that subfolder's slot while the studio serving
// project reads project's store. The rule is a documented contract and stays;
// the hint only makes the split audible. It looks from the anchor's parent up
// to the git root (never past it, so a stray ~/.iterion or the home slot raises
// nothing) for the nearest folder whose resolved store already holds runs/.
// Silent when the caller pinned --store-dir or the anchor carries its own
// managed .iterion — both are explicit choices.
func ancestorStoreHint(anchor, override, resolved string) string {
	if override != "" || anchor == "" {
		return ""
	}
	abs, err := filepath.Abs(anchor)
	if err != nil || filepath.Dir(resolved) == abs {
		return ""
	}
	root := git.FindRepoRoot(abs)
	if root == "" || root == abs {
		return ""
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if cand := store.ResolveStoreDir(dir, ""); cand != resolved {
			if info, statErr := os.Stat(filepath.Join(cand, "runs")); statErr == nil && info.IsDir() {
				return cand
			}
		}
		if dir == root || filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func runStoreHas(storeDir, runID string) bool {
	if storeDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(storeDir, "runs", runID, "run.json"))
	return err == nil
}
