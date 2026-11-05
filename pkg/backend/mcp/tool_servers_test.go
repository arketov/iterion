package mcp

import (
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
)

func toolServersWorkflow(tools ...string) *ir.Workflow {
	return &ir.Workflow{
		Nodes: map[string]ir.Node{
			"a": &ir.AgentNode{BaseNode: ir.BaseNode{ID: "a"}, Tools: tools},
		},
		ResolvedMCPServers: map[string]*ir.MCPServer{"bars": {Name: "bars"}, "other": {Name: "other"}},
	}
}

func TestUnknownToolServerDiagnostics(t *testing.T) {
	wf := toolServersWorkflow(
		"read_file",
		"mcp.bars.bars_rag_search", // declared
		"mcp.other.*",              // declared wildcard
		"mcp.ghost.x",              // unknown
		"mcp.ghost.*",              // unknown wildcard
		"mcp__phantom__y",          // unknown, FQN spelling
		"mcp.iterion.anything",     // reserved internal namespace: never flagged
	)
	got := UnknownToolServerDiagnostics(wf)
	if len(got) != 3 {
		t.Fatalf("got %d diagnostics, want 3: %+v", len(got), got)
	}
	for _, d := range got {
		if d.Code != ir.DiagUnknownMCPServer || d.Severity != ir.SeverityWarning || d.NodeID != "a" {
			t.Errorf("bad diagnostic: %+v", d)
		}
		if !strings.Contains(d.Message, "bars") {
			t.Errorf("message should list the known servers: %s", d.Message)
		}
	}
}

func TestUnknownToolServerDiagnosticsSilentWithoutCatalog(t *testing.T) {
	wf := toolServersWorkflow("mcp.ghost.x")
	wf.ResolvedMCPServers = nil
	if got := UnknownToolServerDiagnostics(wf); len(got) != 0 {
		t.Fatalf("no resolved catalog must not flag anything: %+v", got)
	}
	if got := UnknownToolServerDiagnostics(nil); got != nil {
		t.Fatalf("nil workflow: %+v", got)
	}
}

func TestSkippedPluginRemedyNamesThePluginAndTheCommands(t *testing.T) {
	got := skippedPluginRemedy([]string{"repo-falcon: read plugin.yaml: The system cannot find the file specified."})
	for _, want := range []string{"iterion plugin uninstall repo-falcon", "plugin.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("remedy %q lacks %q", got, want)
		}
	}
	if got := skippedPluginRemedy([]string{"no separator here"}); got != "" {
		t.Errorf("a skip without a plugin name gets no remedy: %q", got)
	}
}
