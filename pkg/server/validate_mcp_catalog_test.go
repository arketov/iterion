package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/backend/mcp"
	"github.com/arketov/iterion/pkg/dsl/ir"
)

func mcpCatalogWorkflow(tools ...string) *ir.Workflow {
	return &ir.Workflow{Nodes: map[string]ir.Node{
		"ask": &ir.AgentNode{BaseNode: ir.BaseNode{ID: "ask"}, Tools: tools},
	}}
}

func writeMCPProjectFile(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestValidateMCPCatalog_AReservedServerIsASkipNotTheEndOfTheCheck is the
// witness of the card: a coordinator named a server "iterion" in the project's
// .mcp.json, /api/validate answered "MCP catalog could not be resolved … uses
// reserved internal name", and the unknown-server check C332 never ran. Now the
// entry is one warning that says how to rename it, and the check runs over the
// rest of the catalog.
func TestValidateMCPCatalog_AReservedServerIsASkipNotTheEndOfTheCheck(t *testing.T) {
	t.Setenv(mcp.EnvAutoLoad, "true")
	dir := t.TempDir()
	writeMCPProjectFile(t, dir, `{"mcpServers":{"iterion":{"type":"stdio","command":"x"},"docs":{"type":"stdio","command":"docs"}}}`)
	s := &Server{cfg: Config{WorkDir: dir}}
	var resp validateResponse

	s.appendMCPCatalogIssues(&resp, mcpCatalogWorkflow("mcp.docs.read", "mcp.ghost.read"), dir)

	var skip, c332 bool
	for _, d := range resp.Issues {
		if strings.Contains(d.Message, "could not be resolved") {
			t.Errorf("the catalog was refused whole again: %+v", d)
		}
		if d.Severity == "warning" && strings.Contains(d.Message, `"iterion"`) && strings.Contains(d.Message, "reserved") && strings.Contains(d.Message, "rename") {
			skip = true
		}
		if d.Code == string(ir.DiagUnknownMCPServer) && strings.Contains(d.Message, `"ghost"`) {
			c332 = true
		}
	}
	if !skip {
		t.Errorf("no warning says the reserved server was skipped and how to rename it: %+v", resp.Issues)
	}
	if !c332 {
		t.Errorf("C332 did not run over the servers that remained: %+v", resp.Issues)
	}
	if len(resp.Warnings) != len(resp.Issues) {
		t.Errorf("warnings %v and issues %+v must list the same entries", resp.Warnings, resp.Issues)
	}
}

// An invalid catalog that is NOT a reserved name (a malformed entry) still ends
// the check with the one explanatory warning, as before.
func TestValidateMCPCatalog_AMalformedEntryStillEndsTheCheckWithOneWarning(t *testing.T) {
	t.Setenv(mcp.EnvAutoLoad, "true")
	dir := t.TempDir()
	writeMCPProjectFile(t, dir, `{"mcpServers":{"broken":{"type":"stdio"}}}`)
	s := &Server{cfg: Config{WorkDir: dir}}
	var resp validateResponse

	s.appendMCPCatalogIssues(&resp, mcpCatalogWorkflow("mcp.ghost.read"), dir)

	var refused int
	for _, d := range resp.Issues {
		if strings.Contains(d.Message, "could not be resolved") {
			refused++
		}
		if d.Code == string(ir.DiagUnknownMCPServer) {
			t.Errorf("C332 ran over a catalog that was never resolved: %+v", d)
		}
	}
	if refused != 1 {
		t.Fatalf("issues = %+v, want the one could-not-be-resolved warning", resp.Issues)
	}
}
