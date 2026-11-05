package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arketov/iterion/pkg/botregistry"
)

// defaultListFilesLimit caps GET /api/files. A project folder can hold
// thousands of workflow files (generated trees, vendored copies); a list
// that long is unusable in the editor's start panel. Override with
// ITERION_STUDIO_FILES_LIMIT (a value <= 0 lifts the cap).
const defaultListFilesLimit = 500

// listFilesLimit reads the cap, falling back to the default on a missing or
// malformed value.
func listFilesLimit() int {
	v := strings.TrimSpace(os.Getenv("ITERION_STUDIO_FILES_LIMIT"))
	if v == "" {
		return defaultListFilesLimit
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultListFilesLimit
	}
	return n
}

// isNestedRepo reports whether dir (not the walk root) is the root of its own
// git checkout — a clone, submodule or linked worktree, all marked by a
// `.git` entry. Its files belong to that project, not to this one.
func isNestedRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// manifestFileName marks a directory as one bot bundle (see botregistry).
const manifestFileName = "manifest.yaml"

// Kinds of a listed file. A bundle is one row however many files it has: its
// main.bot. A file is a single-file bot.
const (
	fileKindBundle = "bundle"
	fileKindFile   = "file"
)

// listWorkflowFiles lists the bots the editor can open for this project, from
// the same places the bot catalog looks in (botregistry.DefaultPaths: bots/,
// examples/, .botz/) plus the single .bot files directly in the project root
// (no recursion there). A bundle — a directory with a manifest.yaml — is one
// row, its main.bot; the rest of its files (lib/, skills, …) are parts of it,
// not bots. Nested git checkouts (a directory holding .git: clone, submodule,
// linked worktree) and hidden directories are never entered. The list stops
// after limit entries (limit <= 0: no cap); truncated reports that.
func listWorkflowFiles(workDir string, limit int) (files []fileEntry, truncated bool) {
	files = []fileEntry{}
	seen := map[string]struct{}{}
	// add returns false once the cap is hit.
	add := func(path, kind string) bool {
		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			return true
		}
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; dup {
			return true
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return true
		}
		if limit > 0 && len(files) >= limit {
			truncated = true
			return false
		}
		seen[rel] = struct{}{}
		files = append(files, fileEntry{Name: rel, Size: info.Size(), Kind: kind})
		return true
	}

	// Single files at the project root, not below it.
	if entries, err := os.ReadDir(workDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !isWorkflowFile(e.Name()) {
				continue
			}
			if !add(filepath.Join(workDir, e.Name()), fileKindFile) {
				return files, truncated
			}
		}
	}

	for _, root := range botregistry.DefaultPaths(workDir) {
		stop := false
		// Per-entry errors are handled in the callback; an absent root or a
		// walk failure yields the partial (possibly empty) list, which this
		// read-only file browser degrades to gracefully.
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && (isSkippedDir(d.Name()) || isNestedRepo(path)) {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, manifestFileName)); err == nil {
					// One bundle: its entry file, not its parts.
					if !add(filepath.Join(path, "main.bot"), fileKindBundle) {
						stop = true
						return fs.SkipAll
					}
					return filepath.SkipDir
				}
				return nil
			}
			if !isWorkflowFile(d.Name()) {
				return nil
			}
			if !add(path, fileKindFile) {
				stop = true
				return fs.SkipAll
			}
			return nil
		})
		if stop {
			return files, truncated
		}
	}

	return files, truncated
}
