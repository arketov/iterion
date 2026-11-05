package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// isBundleFragment reports whether a slash-separated project-relative path
// is a part of a multi-file bot rather than a bot of its own: anything under
// bots/<name>/ except that bot's main.bot (lib/nodes.bot, lib/prompts.bot,
// skills, …). A file directly under bots/ is a bot of its own.
func isBundleFragment(rel string) bool {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		if s != "bots" {
			continue
		}
		rest := segs[i+1:]
		if len(rest) >= 2 {
			return strings.Join(rest[1:], "/") != "main.bot"
		}
	}
	return false
}

// isNestedRepo reports whether dir (not the walk root) is the root of its own
// git checkout — a clone, submodule or linked worktree, all marked by a
// `.git` entry. Its files belong to that project, not to this one.
func isNestedRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// listWorkflowFiles walks workDir for the workflow files the editor can open
// as bots of this project: it does not descend into nested git checkouts or
// hidden/vendor directories, leaves out bundle fragments, and stops after
// limit entries (limit <= 0: no cap). truncated reports that the cap cut the
// list short.
func listWorkflowFiles(workDir string, limit int) (files []fileEntry, truncated bool) {
	files = []fileEntry{}
	// Per-entry errors are handled in the callback; a root-level walk failure
	// yields the partial (possibly empty) list, which this read-only file
	// browser degrades to gracefully.
	_ = filepath.WalkDir(workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == workDir {
				return nil
			}
			if isSkippedDir(d.Name()) || isNestedRepo(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isWorkflowFile(d.Name()) {
			return nil
		}
		rel, rerr := filepath.Rel(workDir, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if isBundleFragment(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if limit > 0 && len(files) >= limit {
			truncated = true
			return fs.SkipAll
		}
		files = append(files, fileEntry{Name: rel, Size: info.Size()})
		return nil
	})
	return files, truncated
}
