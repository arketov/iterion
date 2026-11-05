package botregistry

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arketov/iterion/pkg/dsl/workflowfile"
)

// Bot origins surfaced by the studio's unified bot list. "catalog" is a
// bundle found under the project's discovery roots (bots/, examples/,
// .botz/); the others are the two kinds of bot that have no manifest and
// therefore never come out of List.
const (
	// OriginBuiltin is a recipe embedded in the iterion binary.
	OriginBuiltin = "builtin"
	// OriginFile is a single .bot file in the project that is not part of a
	// manifest bundle ("project files" group in the studio).
	OriginFile = "file"
)

const (
	// maxProjectFileDepth bounds how deep the project walk descends. The
	// limit exists to keep a monorepo scan fast; MaxProjectFiles is the
	// matching count cap. Both are far above a normal project's .bot count.
	maxProjectFileDepth = 8
	// MaxProjectFiles caps how many loose .bot files DiscoverProjectFiles
	// returns, so a pathological tree cannot blow up the response.
	MaxProjectFiles = 500
)

// skippedProjectDirs are directory names the loose-file walk never enters:
// dependency trees and build output that can hold thousands of unrelated
// files. Dot-directories are skipped as a class (.git, .iterion, .worktree,
// .claude, .botz — .botz is already a discovery root).
var skippedProjectDirs = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"target":       {},
	"venv":         {},
	"__pycache__":  {},
	"out":          {},
}

// DiscoverProjectFiles lists the single workflow files under workDir that no
// manifest accounts for: every .bot file whose directory holds no
// manifest.yaml, outside the discovery roots, so that a file like
// `test.bot` or `dev-flow.bot` appears as a bot in the studio instead of
// being invisible. A bundle's own main.bot (its directory has a manifest) is
// excluded — the bundle is already an Entry — and so is anything under one of
// excludeRoots (the catalog roots, which List already covers).
//
// Entries carry Name = the slash-form path without extension (unique within
// the project), Path = absolute path, RelPath = workspace-relative slash
// path. Enabled and ManifestEnabled are true: a file has no manifest, so
// there is no visibility flag to turn off. Result order is by RelPath.
func DiscoverProjectFiles(workDir string, excludeRoots []string) []Entry {
	if workDir == "" {
		return nil
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return nil
	}
	excluded := make([]string, 0, len(excludeRoots))
	for _, r := range excludeRoots {
		if abs, err := filepath.Abs(r); err == nil {
			excluded = append(excluded, abs)
		}
	}
	var out []Entry
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, skip := skippedProjectDirs[name]; skip {
				return filepath.SkipDir
			}
			for _, ex := range excluded {
				if path == ex {
					return filepath.SkipDir
				}
			}
			if rel, err := filepath.Rel(root, path); err == nil {
				if strings.Count(filepath.ToSlash(rel), "/") >= maxProjectFileDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// Only single-file programs: .bot, not packed .botz archives (those
		// are discovered as bundles under .botz/).
		if !strings.EqualFold(filepath.Ext(path), ".bot") || !workflowfile.IsWorkflowFile(path) {
			return nil
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), "manifest.yaml")); err == nil {
			return nil // belongs to a manifest bundle
		}
		if len(out) >= MaxProjectFiles {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		out = append(out, Entry{
			Name:            strings.TrimSuffix(rel, filepath.Ext(rel)),
			Path:            path,
			RelPath:         rel,
			Enabled:         true,
			ManifestEnabled: true,
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}
