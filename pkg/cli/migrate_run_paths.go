package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/arketov/iterion/pkg/store"
)

// botRelocations records the examples/ -> bots/ source-directory move
// (2026-06) for the 9 productised bots. Runs created before the move
// recorded absolute file_path / bundle_path values under examples/<bot>;
// MigrateRunPaths rewrites those to bots/<bot> so the studio "open
// source" link and `iterion resume` find the relocated files. Demos that
// stayed under examples/ (cursors, ultracode, clarify) are NOT remapped.
//
// The leading slash anchors the match to a path boundary: it covers both
// file_path (…/examples/<bot>/main.bot) and a bare bundle_path directory
// (…/examples/<bot>), works on the absolute paths the store records, and
// avoids a false hit on an unrelated dir like "/my-examples/feature_dev".
// No name in the set is a prefix of another, so the remaps never overlap.
var botRelocations = []struct{ from, to string }{
	{"/examples/feature_dev", "/bots/feature-dev"},
	{"/examples/whole_improve_loop", "/bots/whole-improve-loop"},
	{"/examples/branch_improve_loop", "/bots/branch-improve-loop"},
	{"/examples/whats-next", "/bots/whats-next"},
	{"/examples/doc-align", "/bots/docs-refresh"},
	{"/examples/sec-audit-source", "/bots/sec-audit-source"},
	{"/examples/sec-audit-deps", "/bots/sec-audit-deps"},
	{"/examples/secured-renovacy", "/bots/secured-renovacy"},
	// doc-align was renamed to docs-refresh (2026-06). Remap its
	// pre-rename bots/ location too so runs created between the
	// examples→bots move and the rename still resolve the bot. Safe vs
	// the examples/doc-align rule above: "/bots/docs-refresh" does not
	// contain "/bots/doc-align", so neither remap double-applies.
	{"/bots/doc-align", "/bots/docs-refresh"},
	// 3 bots renamed snake->kebab (2026-06): remap their pre-rename bots/ paths.
	// (code_review was also renamed, to review-pr, but with no back-compat
	// remap by design — see the review-pr rename commit.)
	{"/bots/feature_dev", "/bots/feature-dev"},
	{"/bots/whole_improve_loop", "/bots/whole-improve-loop"},
	{"/bots/branch_improve_loop", "/bots/branch-improve-loop"},
}

// remapBotPath applies the relocation to a single recorded path. Returns
// the (possibly) rewritten path and whether it changed. Idempotent: a
// path already under bots/ contains no /examples/<bot> substring.
func remapBotPath(p string) (string, bool) {
	out := p
	for _, r := range botRelocations {
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	return out, out != p
}

// runPathFieldRe matches the JSON file_path / bundle_path string values in
// a run.json so the rewrite is scoped to exactly those two fields and the
// rest of the document is preserved byte-for-byte. Groups: 1=prefix up to
// the opening quote, 2=field name, 3=path value, 4=closing quote.
var runPathFieldRe = regexp.MustCompile(`("(file_path|bundle_path)"\s*:\s*")([^"]*)(")`)

// MigrateRunPathsOptions configures MigrateRunPaths.
type MigrateRunPathsOptions struct {
	// StoreDir is the run store root; runs live under
	// <StoreDir>/runs/<id>/run.json.
	StoreDir string
	// DryRun reports the changes without writing them.
	DryRun bool
}

// RunPathChange is one rewritten field in one run.
type RunPathChange struct {
	RunID string `json:"run_id"`
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// MigrateRunPathsResult summarises a migration pass.
type MigrateRunPathsResult struct {
	Scanned int             `json:"scanned"`
	Updated int             `json:"updated"`
	Changes []RunPathChange `json:"changes"`
}

// MigrateRunPaths rewrites file_path + bundle_path in every
// <StoreDir>/runs/*/run.json that still points at a relocated bot's old
// examples/ source. Idempotent and field-scoped (the rest of each
// run.json is preserved byte-for-byte). A missing runs/ directory is a
// no-op (returns a zero result, no error).
func MigrateRunPaths(opts MigrateRunPathsOptions) (MigrateRunPathsResult, error) {
	var res MigrateRunPathsResult
	runsDir := filepath.Join(opts.StoreDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, fmt.Errorf("migrate run-paths: read %s: %w", runsDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		runJSON := filepath.Join(runsDir, runID, "run.json")
		data, err := os.ReadFile(runJSON)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return res, fmt.Errorf("migrate run-paths: read %s: %w", runJSON, err)
		}
		res.Scanned++

		var pending []RunPathChange
		out := runPathFieldRe.ReplaceAllStringFunc(string(data), func(match string) string {
			m := runPathFieldRe.FindStringSubmatch(match)
			np, ok := remapBotPath(m[3])
			if !ok {
				return match
			}
			pending = append(pending, RunPathChange{
				RunID: runID, Field: m[2], From: m[3], To: np,
			})
			return m[1] + np + m[4]
		})
		if len(pending) == 0 {
			continue
		}
		res.Updated++
		res.Changes = append(res.Changes, pending...)
		if opts.DryRun {
			continue
		}
		if err := store.WriteFileAtomic(runJSON, []byte(out), 0o644); err != nil {
			return res, fmt.Errorf("migrate run-paths: write %s: %w", runJSON, err)
		}
	}
	return res, nil
}
