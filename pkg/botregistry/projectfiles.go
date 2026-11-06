package botregistry

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arketov/iterion/pkg/bundle"
	"github.com/arketov/iterion/pkg/dsl/workflowfile"
)

// Bot origins surfaced by the studio's unified bot list. "catalog" is a
// bundle found under the project's discovery roots (bots/, examples/,
// .botz/); "file" is the kind of bot that has no manifest and therefore
// never comes out of List.
const (
	// OriginFile is a single .bot file in the project that is not part of a
	// manifest bundle ("project files" group in the studio).
	OriginFile = "file"
)

// The project's own workflows, as ONE rule for every surface that lists them
// (the bot catalog's origin=file rows, GET /api/files behind the editor's
// Project files panel and the file picker):
//
//   - the bot sources under the discovery roots (bots/, examples/, .botz/ or
//     the explicit --bots-path roots) that sit inside the project: a bundle
//     is ONE row — its main.bot, never its lib/, skills or presets — and a
//     loose .bot file is one row each, except the fragments of a bot's lib/;
//   - the single .bot files directly in the project root, with no recursion.
//
// Nothing else belongs to the project's own workflows: not a file in
// workflows/ or docs/, not a nested clone of another project
// (build-desktop/src/…), not a hidden directory, not node_modules, not a
// testdata directory (a repository's test inputs). A walk of the whole tree is
// what listed hundreds of fragments, fixtures and foreign copies as "bots".
//
// The same rule governs the bot catalog's walk (discoverBots), so the editor's
// lists and the routable set agree. A walk ROOT is never pruned: naming a
// directory as a root (--bots-path, bots.paths) is the way to reach one of
// these on purpose.

// Kinds of a project workflow row. A bundle is one row however many files it
// has: its main.bot. A file is a single-file bot.
const (
	ProjectWorkflowBundle = "bundle"
	ProjectWorkflowFile   = "file"
)

// ProjectWorkflow is one row of the project's own workflows.
type ProjectWorkflow struct {
	// Path is the absolute path of the file the editor opens: a loose .bot
	// file, or a bundle's main.bot.
	Path string
	// RelPath is Path relative to the project folder, in slash form.
	RelPath string
	// Kind is ProjectWorkflowBundle or ProjectWorkflowFile.
	Kind string
}

// ProjectWorkflows is the result of ListProjectWorkflows.
type ProjectWorkflows struct {
	// Items are the rows kept after the limit: bundles first, then the single
	// files of the project root, then the loose files under the roots; each
	// group by RelPath.
	Items []ProjectWorkflow
	// Total is how many rows the rule found before the limit cut the list.
	Total int
	// Truncated reports that the limit cut the list (Total > len(Items)).
	Truncated bool
	// Skipped are the sources the walk saw and could not list: a directory
	// claimed as a bundle that carries no main.bot (kind "unopenable"), or a
	// path the walk could not read (kind "walk"). Paths are relative to the
	// project folder when they sit inside it.
	Skipped []DiscoveryError
}

// walkSkippedDir reports a directory NAME no walk of the discovery roots (the
// catalog's and the project list's) enters below a root: hidden directories (a
// scaffold's staging directory among them), dependency trees and testdata,
// where a repository keeps its test inputs. The match is exact and lowercase.
func walkSkippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata"
}

// isNestedRepo reports whether dir (not the walk root) is the root of its own
// git checkout — a clone, submodule or linked worktree, all marked by a
// `.git` entry. Its files belong to that project, not to this one. Both the
// catalog walk and the project list apply it, after the bundle claim.
func isNestedRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// projectRootFiles returns the absolute paths of the single .bot files
// directly in workDir that the project-root rule contributes: none when a
// catalog root is workDir itself (or holds it — `--bots-path .`: the catalog
// already walks every file there), none when workDir is itself a bundle (its
// root files are the bundle's parts), and never a file that is itself a
// catalog root.
func projectRootFiles(workDir string, roots []string) []string {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil
	}
	for _, r := range roots {
		if st, err := os.Stat(r); err == nil && st.IsDir() && pathUnder(workDir, r) {
			return nil
		}
	}
	// Only a folder that really opens as a bundle (claimed AND main.bot beside
	// it) owns its root files; a skills/ or presets/ directory alone, with no
	// main.bot, makes no bundle and its neighbours stay loose files.
	if claimed, openable, _ := bundle.DirClaim(workDir); claimed && openable {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !workflowfile.IsWorkflowFile(e.Name()) {
			continue
		}
		p := filepath.Join(workDir, e.Name())
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			continue
		}
		isRoot := false
		for _, r := range roots {
			if samePath(p, r) {
				isRoot = true
				break
			}
		}
		if isRoot {
			continue
		}
		out = append(out, p)
	}
	return out
}

// RootWorkflowFiles lists the single .bot files directly in workDir that no
// manifest accounts for: the catalog's origin=file rows. Only the project
// root is read, with no recursion — a bundle's own main.bot, the files under
// a discovery root (the catalog lists those as bots) and everything deeper
// are not the project's loose files. roots are the catalog's discovery roots
// (bots/, examples/, .botz/ or --bots-path).
//
// Entries carry Name = the file name without extension, Path = absolute
// path, RelPath = the file name. Enabled and ManifestEnabled are true: a file
// has no manifest, so there is no visibility flag to turn off. Result order
// is by RelPath.
func RootWorkflowFiles(workDir string, roots []string) []Entry {
	if workDir == "" {
		return nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, p := range projectRootFiles(abs, roots) {
		rel := filepath.ToSlash(filepath.Base(p))
		out = append(out, Entry{
			Name:            strings.TrimSuffix(rel, filepath.Ext(rel)),
			Path:            p,
			RelPath:         rel,
			Enabled:         true,
			ManifestEnabled: true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}

// ListProjectWorkflows lists the project's own workflows by the rule above:
// the project root's single .bot files plus the bot sources under roots that
// sit inside workDir. A root outside workDir is not listed — the editor
// refuses to open a path outside the project. limit caps the rows kept
// (limit <= 0: no cap); the cap is applied AFTER the rows are ordered
// (bundles first), so a project with many loose files still lists its
// bundles, and Total/Truncated say what the cap cut.
//
// The directory judgement is the catalog's: bundle.DirClaim decides what a
// bundle is, a lib/ beside a workflow is a bot's fragments, and a nested git
// checkout (a directory holding .git that is not itself a bundle) is another
// project's tree.
func ListProjectWorkflows(workDir string, roots []string, limit int) ProjectWorkflows {
	var res ProjectWorkflows
	if workDir == "" {
		return res
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return res
	}
	workDir = abs

	rel := func(path string) (string, bool) {
		r, err := filepath.Rel(workDir, path)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", false
		}
		return filepath.ToSlash(r), true
	}
	seen := map[string]struct{}{}
	var bundles, rootFiles, otherFiles []ProjectWorkflow
	add := func(dst *[]ProjectWorkflow, path, kind string) {
		r, ok := rel(path)
		if !ok {
			return
		}
		key := PathKey(path)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		*dst = append(*dst, ProjectWorkflow{Path: path, RelPath: r, Kind: kind})
	}
	skip := func(path string, kind DiscoveryErrorKind, msg string) {
		shown := path
		if r, ok := rel(path); ok {
			shown = r
		}
		res.Skipped = append(res.Skipped, DiscoveryError{Path: shown, Error: msg, Kind: kind})
	}

	for _, p := range projectRootFiles(workDir, roots) {
		add(&rootFiles, p, ProjectWorkflowFile)
	}
	if claimed, openable, _ := bundle.DirClaim(workDir); claimed && openable {
		add(&bundles, filepath.Join(workDir, bundle.MainBotFile), ProjectWorkflowBundle)
	}

	for _, root := range roots {
		// A relative --bots-path (`flows`) walks to relative paths, which
		// cannot be made workDir-relative: walk the absolute spelling.
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		info, err := os.Stat(root)
		if err != nil {
			// An absent root is the normal case for the optimistic defaults.
			continue
		}
		if !pathUnder(root, workDir) {
			continue
		}
		if !info.IsDir() {
			if workflowfile.IsWorkflowFile(root) {
				add(&otherFiles, root, ProjectWorkflowFile)
			}
			continue
		}
		var fragmentDirs []string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// One unreadable branch must not blank the rest of the list.
				// The error text names absolute paths: keep the project-relative
				// spelling, as the catalog's diagnostics do.
				skip(path, DiscoveryErrorWalk, strings.ReplaceAll(walkErr.Error(), workDir+string(filepath.Separator), ""))
				return nil
			}
			if d.IsDir() {
				if path != root && walkSkippedDir(d.Name()) {
					return filepath.SkipDir
				}
				if path != root && isFragmentDir(path) {
					fragmentDirs = append(fragmentDirs, path)
					return nil
				}
				claimed, openable, _ := bundle.DirClaim(path)
				if claimed {
					if !openable {
						skip(path, DiscoveryErrorUnopenable, "the directory is claimed as a bundle and carries no "+bundle.MainBotFile)
						return filepath.SkipDir
					}
					// One bundle: its entry file, not its parts. A bundle that
					// is a git clone is still a bundle — the catalog lists it.
					add(&bundles, filepath.Join(path, bundle.MainBotFile), ProjectWorkflowBundle)
					return filepath.SkipDir
				}
				if path != root && isNestedRepo(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if !workflowfile.IsWorkflowFile(d.Name()) || underAny(path, fragmentDirs) {
				return nil
			}
			add(&otherFiles, path, ProjectWorkflowFile)
			return nil
		})
	}

	byRel := func(s []ProjectWorkflow) {
		sort.Slice(s, func(i, j int) bool { return s[i].RelPath < s[j].RelPath })
	}
	byRel(bundles)
	byRel(rootFiles)
	byRel(otherFiles)
	all := make([]ProjectWorkflow, 0, len(bundles)+len(rootFiles)+len(otherFiles))
	all = append(all, bundles...)
	all = append(all, rootFiles...)
	all = append(all, otherFiles...)

	res.Total = len(all)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
		res.Truncated = true
	}
	res.Items = all
	return res
}
