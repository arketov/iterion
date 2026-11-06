package server

import (
	"os"
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

// Kinds of a listed file. A bundle is one row however many files it has: its
// main.bot. A file is a single-file bot.
const (
	fileKindBundle = botregistry.ProjectWorkflowBundle
	fileKindFile   = botregistry.ProjectWorkflowFile
)

// listWorkflowFiles lists the bots the editor can open for this project: the
// project's own workflows by botregistry.ListProjectWorkflows — the one rule
// the bot catalog's origin=file rows follow too (the catalog roots, so
// --bots-path included, plus the single .bot files directly in the project
// root; a bundle is one row, its main.bot; nested git checkouts and hidden
// directories are never entered). The rows are ordered bundles first, so the
// cap never drops the bundles for loose files. roots are the catalog's
// discovery roots (Server.effectivePathsFor). The cap is limit rows
// (limit <= 0: no cap); total is how many rows the rule found, truncated
// reports that the cap cut the list, and skipped names the bundle
// directories that carry no main.bot (they cannot be opened, and the catalog
// reports them too).
func listWorkflowFiles(workDir string, roots []string, limit int) (files []fileEntry, total int, truncated bool, skipped []botregistry.DiscoveryError) {
	files = []fileEntry{}
	res := botregistry.ListProjectWorkflows(workDir, roots, limit)
	for _, it := range res.Items {
		var size int64
		if info, err := os.Stat(it.Path); err == nil {
			size = info.Size()
		}
		files = append(files, fileEntry{Name: it.RelPath, Size: size, Kind: it.Kind})
	}
	return files, res.Total, res.Truncated, res.Skipped
}
