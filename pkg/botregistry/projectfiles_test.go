package botregistry

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func writeProjectFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundleAt writes a manifest bundle (manifest.yaml + main.bot) under dir.
func bundleAt(t *testing.T, dir string) {
	t.Helper()
	writeProjectFile(t, filepath.Join(dir, "manifest.yaml"), "name: "+filepath.Base(dir)+"\n")
	writeProjectFile(t, filepath.Join(dir, "main.bot"), "workflow w:\n")
}

func relsOf(items []ProjectWorkflow) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.RelPath)
	}
	return out
}

func defaultRootsOf(root string) []string { return DefaultPaths(root) }

func TestRootWorkflowFiles_OnlyTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "test.bot"), "workflow t:\n")
	writeProjectFile(t, filepath.Join(root, "dev-flow.bot"), "workflow d:\n")
	// Not the project's loose files: deeper in the tree, a nested clone, a
	// bundle's parts, a catalog root's content.
	writeProjectFile(t, filepath.Join(root, "flows", "deep.bot"), "")
	writeProjectFile(t, filepath.Join(root, "build-desktop", "src", "bots", "y", "main.bot"), "")
	writeProjectFile(t, filepath.Join(root, "bots", "x", "lib", "nodes.bot"), "")
	writeProjectFile(t, filepath.Join(root, "bots", "solo.bot"), "")
	writeProjectFile(t, filepath.Join(root, "testdata", "input_mapping", "a.bot"), "")
	writeProjectFile(t, filepath.Join(root, "notes.txt"), "")

	got := RootWorkflowFiles(root, defaultRootsOf(root))
	var rels []string
	for _, e := range got {
		rels = append(rels, e.RelPath)
		if !e.Enabled || !e.ManifestEnabled {
			t.Errorf("%s: loose files report enabled", e.RelPath)
		}
		if !filepath.IsAbs(e.Path) {
			t.Errorf("%s: Path must be absolute, got %s", e.RelPath, e.Path)
		}
	}
	if want := []string{"dev-flow.bot", "test.bot"}; !reflect.DeepEqual(rels, want) {
		t.Fatalf("rels = %v, want %v", rels, want)
	}
	if got[0].Name != "dev-flow" || got[1].Name != "test" {
		t.Fatalf("names = %q, %q", got[0].Name, got[1].Name)
	}
}

func TestRootWorkflowFiles_EmptyWorkDir(t *testing.T) {
	if got := RootWorkflowFiles("", nil); got != nil {
		t.Fatalf("empty workDir must yield nil, got %v", got)
	}
}

// `--bots-path .` makes the project folder itself a catalog root: the catalog
// already lists every file there, so no file is listed a second time.
func TestRootWorkflowFiles_NothingWhenTheProjectIsACatalogRoot(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "a.bot"), "workflow a:\n")
	if got := RootWorkflowFiles(root, []string{root}); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestRootWorkflowFiles_ARootThatIsAFileIsNotListedTwice(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "a.bot"), "workflow a:\n")
	writeProjectFile(t, filepath.Join(root, "b.bot"), "workflow b:\n")
	got := RootWorkflowFiles(root, []string{filepath.Join(root, "a.bot")})
	var rels []string
	for _, e := range got {
		rels = append(rels, e.RelPath)
	}
	if want := []string{"b.bot"}; !reflect.DeepEqual(rels, want) {
		t.Fatalf("rels = %v, want %v", rels, want)
	}
}

// A project folder that is itself a bundle: its root .bot files are the
// bundle's parts, not loose bots.
func TestRootWorkflowFiles_ABundleFolderHasNoLooseFiles(t *testing.T) {
	root := t.TempDir()
	bundleAt(t, root)
	writeProjectFile(t, filepath.Join(root, "extra.bot"), "workflow e:\n")
	if got := RootWorkflowFiles(root, nil); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestListProjectWorkflows_Scope(t *testing.T) {
	root := t.TempDir()
	// Root: single files only, no recursion.
	writeProjectFile(t, filepath.Join(root, "dev-flow.bot"), "workflow d:\n")
	writeProjectFile(t, filepath.Join(root, "elsewhere", "deep.bot"), "workflow e:\n")
	// bots/: a bundle is one row (main.bot); its lib/ parts are not rows.
	bundleAt(t, filepath.Join(root, "bots", "x"))
	writeProjectFile(t, filepath.Join(root, "bots", "x", "lib", "nodes.bot"), "")
	writeProjectFile(t, filepath.Join(root, "bots", "review.bot"), "workflow r:\n")
	// The fragments of a bot that is a plain main.bot plus lib/.
	writeProjectFile(t, filepath.Join(root, "bots", "multi", "main.bot"), "workflow m:\n")
	writeProjectFile(t, filepath.Join(root, "bots", "multi", "lib", "prompts.bot"), "")
	// examples/ and .botz/ are discovery roots too.
	writeProjectFile(t, filepath.Join(root, "examples", "demo.bot"), "workflow d:\n")
	bundleAt(t, filepath.Join(root, ".botz", "packed"))
	// A nested clone and a linked worktree inside a root: each holds .git.
	writeProjectFile(t, filepath.Join(root, "bots", "clone", "a.bot"), "")
	if err := os.MkdirAll(filepath.Join(root, "bots", "clone", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectFile(t, filepath.Join(root, "examples", "wt", "b.bot"), "")
	writeProjectFile(t, filepath.Join(root, "examples", "wt", ".git"), "gitdir: x") // .git as a file
	// A bundle that is itself a git clone is still a bundle.
	bundleAt(t, filepath.Join(root, "bots", "cloned-bundle"))
	if err := os.MkdirAll(filepath.Join(root, "bots", "cloned-bundle", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A nested clone of the whole project below the root is never reached.
	writeProjectFile(t, filepath.Join(root, "build-desktop", "src", "bots", "y", "main.bot"), "")

	res := ListProjectWorkflows(root, defaultRootsOf(root), 0)
	if res.Truncated || res.Total != len(res.Items) {
		t.Fatalf("unexpected truncation: total=%d items=%d truncated=%v", res.Total, len(res.Items), res.Truncated)
	}
	got := map[string]string{}
	for _, it := range res.Items {
		got[it.RelPath] = it.Kind
	}
	want := map[string]string{
		"bots/x/main.bot":             ProjectWorkflowBundle,
		"bots/multi/main.bot":         ProjectWorkflowFile,
		"bots/cloned-bundle/main.bot": ProjectWorkflowBundle,
		"bots/review.bot":             ProjectWorkflowFile,
		"examples/demo.bot":           ProjectWorkflowFile,
		".botz/packed/main.bot":       ProjectWorkflowBundle,
		"dev-flow.bot":                ProjectWorkflowFile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListProjectWorkflows_RootWithGitIsStillListed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectFile(t, filepath.Join(root, "a.bot"), "workflow a:\n")
	res := ListProjectWorkflows(root, defaultRootsOf(root), 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"a.bot"}) {
		t.Fatalf("got %v", got)
	}
}

// A bundle whose manifest has no main.bot cannot be opened: it is reported,
// not silently dropped (the catalog reports it too).
func TestListProjectWorkflows_AnUnopenableBundleIsReported(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "bots", "broken", "manifest.yaml"), "name: broken\n")
	bundleAt(t, filepath.Join(root, "bots", "fine"))
	res := ListProjectWorkflows(root, defaultRootsOf(root), 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"bots/fine/main.bot"}) {
		t.Fatalf("items = %v", got)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Path != "bots/broken" || res.Skipped[0].Kind != DiscoveryErrorUnopenable {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

// The cap is applied after the rows are ordered: bundles survive it, loose
// files are what it cuts, and Total says what the rule found.
func TestListProjectWorkflows_TheCapKeepsBundlesFirst(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.bot", "b.bot", "c.bot"} {
		writeProjectFile(t, filepath.Join(root, n), "workflow w:\n")
	}
	bundleAt(t, filepath.Join(root, ".botz", "packed"))

	res := ListProjectWorkflows(root, defaultRootsOf(root), 2)
	if !res.Truncated || res.Total != 4 {
		t.Fatalf("truncated=%v total=%d, want true, 4", res.Truncated, res.Total)
	}
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{".botz/packed/main.bot", "a.bot"}) {
		t.Fatalf("items = %v", got)
	}
	res = ListProjectWorkflows(root, defaultRootsOf(root), 4)
	if res.Truncated || len(res.Items) != 4 {
		t.Fatalf("at exactly the cap: %d items, truncated=%v", len(res.Items), res.Truncated)
	}
	res = ListProjectWorkflows(root, defaultRootsOf(root), 0)
	if res.Truncated || len(res.Items) != 4 {
		t.Fatalf("no cap: %d items, truncated=%v", len(res.Items), res.Truncated)
	}
}

func TestListProjectWorkflows_FollowsExplicitRoots(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "flows", "dev-flow.bot"), "workflow d:\n")
	writeProjectFile(t, filepath.Join(root, "bots", "ignored.bot"), "workflow i:\n")
	res := ListProjectWorkflows(root, []string{filepath.Join(root, "flows")}, 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"flows/dev-flow.bot"}) {
		t.Fatalf("got %v", got)
	}
}

// The editor refuses a path outside the project, so a root outside it is not
// a source of rows.
func TestListProjectWorkflows_ARootOutsideTheProjectIsNotListed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeProjectFile(t, filepath.Join(outside, "x.bot"), "workflow x:\n")
	res := ListProjectWorkflows(root, []string{outside}, 0)
	if len(res.Items) != 0 {
		t.Fatalf("got %v", relsOf(res.Items))
	}
}

func TestListProjectWorkflows_EmptyWorkDir(t *testing.T) {
	res := ListProjectWorkflows("", nil, 0)
	if res.Items != nil || res.Total != 0 {
		t.Fatalf("got %+v", res)
	}
}

// A skills/ directory in the project root with no main.bot beside it makes no
// bundle: the project's loose files stay listed (this very repository has a
// root skills/ folder).
func TestRootWorkflowFiles_ASkillsFolderAloneDoesNotHideTheLooseFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectFile(t, filepath.Join(root, "flow.bot"), "workflow f:\n")
	got := RootWorkflowFiles(root, defaultRootsOf(root))
	if len(got) != 1 || got[0].RelPath != "flow.bot" {
		t.Fatalf("got %+v", got)
	}
	res := ListProjectWorkflows(root, defaultRootsOf(root), 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"flow.bot"}) {
		t.Fatalf("items = %v", got)
	}
}

// --bots-path may be relative (the flag is passed as typed): the walk still
// lists the files, project-relative.
func TestListProjectWorkflows_ARelativeRootIsListed(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "flows", "dev-flow.bot"), "workflow d:\n")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	res := ListProjectWorkflows(".", []string{"flows"}, 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"flows/dev-flow.bot"}) {
		t.Fatalf("got %v", got)
	}
}

// The bot catalog (discoverBots) and the project list (ListProjectWorkflows)
// walk the discovery roots by ONE rule: neither enters a hidden directory,
// node_modules, testdata or a nested git checkout, and a bundle that is itself
// a clone is still a bundle in both.
func TestCatalogAndProjectListWalkTheSameTree(t *testing.T) {
	root := t.TempDir()
	body := "workflow w:\n"
	mkGit := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Kept.
	bundleAt(t, filepath.Join(root, "bots", "x"))
	writeProjectFile(t, filepath.Join(root, "bots", "x", "lib", "n.bot"), body)
	writeProjectFile(t, filepath.Join(root, "bots", "review.bot"), body)
	writeProjectFile(t, filepath.Join(root, "bots", "multi", "main.bot"), body)
	writeProjectFile(t, filepath.Join(root, "bots", "multi", "lib", "p.bot"), body)
	writeProjectFile(t, filepath.Join(root, "examples", "demo.bot"), body)
	bundleAt(t, filepath.Join(root, ".botz", "packed"))
	bundleAt(t, filepath.Join(root, "bots", "cloned-bundle"))
	mkGit(filepath.Join(root, "bots", "cloned-bundle"))
	// Never listed: a nested clone, a linked worktree, hidden directories, a
	// scaffold staging directory, dependencies and test inputs.
	writeProjectFile(t, filepath.Join(root, "bots", "clone", "a.bot"), body)
	mkGit(filepath.Join(root, "bots", "clone"))
	writeProjectFile(t, filepath.Join(root, "examples", "wt", "b.bot"), body)
	writeProjectFile(t, filepath.Join(root, "examples", "wt", ".git"), "gitdir: x")
	writeProjectFile(t, filepath.Join(root, "bots", ".hid", "h.bot"), body)
	bundleAt(t, filepath.Join(root, "bots", ".hidbundle"))
	bundleAt(t, filepath.Join(root, "bots", ".x.scaffold-1"))
	writeProjectFile(t, filepath.Join(root, "bots", "node_modules", "n2.bot"), body)
	writeProjectFile(t, filepath.Join(root, "bots", "testdata", "t.bot"), body)
	writeProjectFile(t, filepath.Join(root, "bots", "testdata", "sub", "u.bot"), body)
	writeProjectFile(t, filepath.Join(root, "examples", "deep", "testdata", "v.bot"), body)

	want := []string{
		".botz/packed/main.bot",
		"bots/cloned-bundle/main.bot",
		"bots/multi/main.bot",
		"bots/review.bot",
		"bots/x/main.bot",
		"examples/demo.bot",
	}

	entries, diags, err := ListWithDiagnostics(ListOptions{Paths: DefaultPaths(root)})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("catalog diagnostics = %+v, want none", diags)
	}
	var catalog []string
	for _, e := range entries {
		rel, err := filepath.Rel(root, e.MainFile())
		if err != nil {
			t.Fatal(err)
		}
		catalog = append(catalog, filepath.ToSlash(rel))
	}
	sort.Strings(catalog)

	list := relsOf(ListProjectWorkflows(root, DefaultPaths(root), 0).Items)
	sort.Strings(list)

	if !reflect.DeepEqual(catalog, want) {
		t.Errorf("catalog = %v, want %v", catalog, want)
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("project list = %v, want %v", list, want)
	}
}

// The one place the two surfaces differ, by design: the catalog answers "what
// is routable", so a bundle whose manifest does not load is a discovery error;
// the project list answers "what the editor can open", so the same bundle
// stays a row — the editor is where it is repaired.
func TestCatalogAndProjectListDifferOnABrokenManifestByDesign(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "bots", "bad", "manifest.yaml"), `name: bad
chat:
  nodes:
    chat:
      kind: human
`)
	writeProjectFile(t, filepath.Join(root, "bots", "bad", "main.bot"), "agent x:\n  model: \"test\"\n")

	entries, diags, err := ListWithDiagnostics(ListOptions{Paths: DefaultPaths(root)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("catalog entries = %+v, want none", entries)
	}
	if len(diags) != 1 || diags[0].Kind != DiscoveryErrorBundle || !strings.HasSuffix(filepath.ToSlash(diags[0].Path), "bots/bad") {
		t.Fatalf("catalog diags = %+v, want one bundle diagnostic for bots/bad", diags)
	}

	res := ListProjectWorkflows(root, DefaultPaths(root), 0)
	if got := relsOf(res.Items); !reflect.DeepEqual(got, []string{"bots/bad/main.bot"}) {
		t.Fatalf("project list = %v, want the broken bundle as a row", got)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", res.Skipped)
	}
}
