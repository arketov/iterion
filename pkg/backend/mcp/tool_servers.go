package mcp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/arketov/iterion/pkg/backend/permission"
	"github.com/arketov/iterion/pkg/backend/tool"
	"github.com/arketov/iterion/pkg/dsl/ir"
)

// UnknownToolServerDiagnostics is C337: one warning per node entry in `tools:`
// that names an MCP server — `mcp.<server>.<tool>`, `mcp.<server>.*` or the
// `mcp__<server>__<tool>` spelling — absent from the catalog PrepareWorkflow
// resolved (wf.ResolvedMCPServers: project `.mcp.json`, enabled plugins, the
// workflow's own `mcp_server` blocks).
//
// It must run AFTER PrepareWorkflow: the compiler has no catalog, which is why
// this check is not in pkg/dsl/ir. Without the catalog (nil ResolvedMCPServers,
// PrepareWorkflow not run or failed) it returns nothing rather than flagging
// every server. A server in iterion's reserved internal namespace
// (permission.IsReservedMCPServerName) is the engine's own and never flagged.
// The same server named twice on one node warns once per distinct entry, so
// each offending line is visible.
func UnknownToolServerDiagnostics(wf *ir.Workflow) []ir.Diagnostic {
	if wf == nil || wf.ResolvedMCPServers == nil {
		return nil
	}
	known := make([]string, 0, len(wf.ResolvedMCPServers))
	for name := range wf.ResolvedMCPServers {
		known = append(known, name)
	}
	slices.Sort(known)

	var out []ir.Diagnostic
	for _, id := range sortedNodeIDs(wf) {
		node := wf.Nodes[id]
		llm, ok := node.(interface{ GetTools() []string })
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, name := range llm.GetTools() {
			server, ok := toolServerOf(name)
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			if _, declared := wf.ResolvedMCPServers[server]; declared || permission.IsReservedMCPServerName(server) {
				continue
			}
			where := "no `mcp_server` block, `.mcp.json` entry or enabled plugin declares it"
			if len(known) > 0 {
				where += "; known servers: " + strings.Join(known, ", ")
			} else {
				where += "; the catalog is empty"
			}
			out = append(out, ir.Diagnostic{
				Code:     ir.DiagUnknownMCPServer,
				Severity: ir.SeverityWarning,
				NodeID:   id,
				Message:  fmt.Sprintf("node %q: tool %q names MCP server %q, but %s — at run time the node fails to build or runs without the tool", id, name, server, where),
				Hint:     ir.HintFor(ir.DiagUnknownMCPServer),
			})
		}
	}
	return out
}

// toolServerOf returns the MCP server a `tools:` entry names, in either
// spelling the registry resolves, or ok=false for a non-MCP entry.
func toolServerOf(name string) (string, bool) {
	if tool.IsMCPWildcard(name) {
		s, err := tool.ParseMCPWildcard(name)
		return s, err == nil
	}
	return tool.MCPServerOf(name)
}

func sortedNodeIDs(wf *ir.Workflow) []string {
	ids := make([]string, 0, len(wf.Nodes))
	for id := range wf.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
