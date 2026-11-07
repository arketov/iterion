package model

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/arketov/iterion/pkg/backend/modelroute"
	"github.com/arketov/iterion/pkg/backend/modelspecs"
	"github.com/arketov/iterion/pkg/dsl/ir"
)

// UnknownModelDiagnostics is C338: one warning per literal `anthropic/<id>` or
// `openai/<id>` model on an agent, judge, LLM router or `fallbacks:` route that
// neither the embedded models.dev snapshot nor the live/cached model table
// lists — the typo that reads "usable" (a credential is all `usable` checks)
// and fails only at the first paid call.
//
// It runs AFTER Compile, from `iterion validate` and POST /api/validate: the
// compiler cannot ask the registry, because this package imports pkg/dsl/ir.
// The snapshot is asked first — deterministic, no I/O, unaffected by
// ITERION_MODEL_SPECS=off — and the live table only on a snapshot miss, so a
// valid model never starts the registry's background fetch.
//
// Only the two providers whose ids the snapshot lists are checked. Bare ids,
// gateway and other providers' routes, `${…}` / `{{…}}` templates and
// `anthropic/*glm*` (z.ai's GLM rides the Anthropic-compatible endpoint and the
// snapshot files it under other providers) stay silent.
func UnknownModelDiagnostics(wf *ir.Workflow) []ir.Diagnostic {
	return unknownModelDiagnostics(wf, func(provider, id string) bool {
		_, ok := modelspecs.Default().Lookup(provider, id)
		return ok
	})
}

// unknownModelDiagnostics is UnknownModelDiagnostics with the live table as a
// seam for tests.
func unknownModelDiagnostics(wf *ir.Workflow, live func(provider, id string) bool) []ir.Diagnostic {
	if wf == nil {
		return nil
	}
	asOf := "unknown"
	if hdr, _ := modelspecs.SnapshotInfo(); len(hdr.AsOf) >= 10 {
		asOf = hdr.AsOf[:10]
	}

	var out []ir.Diagnostic
	for _, id := range slices.Sorted(maps.Keys(wf.Nodes)) {
		node := wf.Nodes[id]
		type site struct{ label, spec string }
		var sites []site
		switch n := node.(type) {
		case *ir.AgentNode:
			sites = append(sites, site{"", n.Model})
		case *ir.JudgeNode:
			sites = append(sites, site{"", n.Model})
		case *ir.RouterNode:
			if n.RouterMode == ir.RouterLLM {
				sites = append(sites, site{"", n.Model})
			}
		}
		if fb, ok := node.(interface{ GetFallbacks() []ir.Fallback }); ok {
			for _, f := range fb.GetFallbacks() {
				if f.Action != ir.FallbackActionSkip && f.Model != "" {
					sites = append(sites, site{fmt.Sprintf("fallback %q ", f.Name), f.Model})
				}
			}
		}
		seen := map[string]bool{}
		for _, s := range sites {
			provider, modelID, unknown := unknownModelOf(s.spec, live)
			if !unknown || seen[s.spec] {
				continue
			}
			seen[s.spec] = true
			out = append(out, ir.Diagnostic{
				Code:     ir.DiagUnknownModel,
				Severity: ir.SeverityWarning,
				NodeID:   id,
				Message: fmt.Sprintf("node %q: %smodel %q is not in the model registry (neither the live table nor the embedded models.dev snapshot as of %s lists %s/%s) — a typo reads \"usable\" and fails only at the first paid call",
					id, s.label, strings.TrimSpace(s.spec), asOf, provider, modelID),
				Hint: ir.HintFor(ir.DiagUnknownModel),
			})
		}
	}
	return out
}

// unknownModelOf reports whether spec is a literal anthropic/openai model the
// registry does not list, and the provider and id it was looked up under.
func unknownModelOf(spec string, live func(provider, id string) bool) (provider, id string, unknown bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.Contains(spec, "${") || strings.Contains(spec, "{{") {
		return "", "", false
	}
	r := modelroute.Parse(spec)
	if r.Provider != "anthropic" && r.Provider != "openai" {
		return "", "", false
	}
	id = r.Wire
	// A trailing variant suffix ("claude-opus-5-5[1m]") is not part of the id.
	if i := strings.Index(id, "["); i > 0 {
		id = id[:i]
	}
	if id == "" || (r.Provider == "anthropic" && strings.Contains(strings.ToLower(id), "glm")) {
		return "", "", false
	}
	if _, ok := modelspecs.SnapshotLookupExact(r.Provider, id); ok {
		return "", "", false
	}
	if live(r.Provider, id) {
		return "", "", false
	}
	return r.Provider, id, true
}
