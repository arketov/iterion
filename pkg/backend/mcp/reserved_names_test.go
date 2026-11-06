package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
)

// A server the BOT declares under a reserved name is refused whole: its author
// chose the name. A project `.mcp.json` entry of the same name is not the bot's
// to fix, and is left out of the catalog instead (see
// TestPrepareWorkflowSkipsAReservedProjectServerAndKeepsTheRest).
func TestPrepareWorkflowRefusesInternalMCPNames(t *testing.T) {
	t.Setenv(EnvAutoLoad, "true")
	for _, name := range []string{"iterion", "iterion_board", "iterion_runs", "Iterion.Anything", "iterionish", "__iterion", ".iterion", "", "__", "."} {
		for _, source := range []string{"explicit-key", "explicit-name"} {
			t.Run(source+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				wf := &ir.Workflow{ActiveMCPServers: []string{"unchanged"}}
				key, cfgName := name, "ordinary"
				if source == "explicit-name" {
					key, cfgName = "ordinary", name
				}
				wf.MCPServers = map[string]*ir.MCPServer{key: {Name: cfgName, Transport: ir.MCPTransportStdio, Command: "untrusted"}}
				err := PrepareWorkflow(wf, dir, dir)
				if err == nil || !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "rename") {
					t.Fatalf("want actionable reserved-name refusal, got %v", err)
				}
				if len(wf.ActiveMCPServers) != 1 || wf.ActiveMCPServers[0] != "unchanged" || wf.ResolvedMCPServers != nil {
					t.Fatal("refusal published a partial workflow catalog")
				}
			})
		}
	}
}

// TestPrepareWorkflowSkipsAReservedProjectServerAndKeepsTheRest is the witness
// of the card: one `.mcp.json` entry named "iterion" took the whole catalog down
// (the studio then warned once and the unknown-server check C337 vanished).
// Now the entry is left out — it can place no tool in the infrastructure
// namespace, which is all the refusal protected — the others resolve, and the
// skip is reported with how to rename it.
func TestPrepareWorkflowSkipsAReservedProjectServerAndKeepsTheRest(t *testing.T) {
	t.Setenv(EnvAutoLoad, "true")
	for _, name := range []string{"iterion", "iterion_board", "Iterion.Anything", "__iterion", ".iterion", "", "__", "."} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
				name:   map[string]string{"type": "stdio", "command": "untrusted"},
				"docs": map[string]string{"type": "stdio", "command": "docs"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			writeProjectMCPFile(t, dir, string(data))
			wf := &ir.Workflow{}

			skipped, err := PrepareWorkflowReport(wf, dir, dir)
			if err != nil {
				t.Fatalf("a reserved project entry must not refuse the catalog: %v", err)
			}
			if len(skipped) != 1 || skipped[0].Name != name || skipped[0].Origin != OriginProject {
				t.Fatalf("skipped = %+v, want the one reserved project entry", skipped)
			}
			msg := skipped[0].Message()
			for _, want := range []string{"reserved", "rename", ".mcp.json", "other servers"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q lacks %q", msg, want)
				}
			}
			if _, kept := wf.ResolvedMCPServers[name]; kept {
				t.Errorf("the reserved server is in the catalog: %v", wf.ResolvedMCPServers)
			}
			if _, ok := wf.ResolvedMCPServers["docs"]; !ok {
				t.Errorf("the other server was dropped with it: %v", wf.ResolvedMCPServers)
			}
			assertStringSliceEq(t, wf.ActiveMCPServers, []string{"docs"})
		})
	}
}

// The unknown-server check keeps working on what is left: a node naming a
// server the catalog lacks is still flagged beside the skip, and the reserved
// name itself is never flagged (it is the engine's own namespace).
func TestUnknownToolServerDiagnosticsStillRunAfterASkip(t *testing.T) {
	t.Setenv(EnvAutoLoad, "true")
	dir := t.TempDir()
	writeProjectMCPFile(t, dir, `{"mcpServers":{"iterion":{"type":"stdio","command":"x"},"docs":{"type":"stdio","command":"docs"}}}`)
	wf := &ir.Workflow{Nodes: map[string]ir.Node{
		"ask": &ir.AgentNode{BaseNode: ir.BaseNode{ID: "ask"}, Tools: []string{"mcp.docs.read", "mcp.barz.read", "mcp.iterion.x"}},
	}}

	if _, err := PrepareWorkflowReport(wf, dir, dir); err != nil {
		t.Fatal(err)
	}
	diags := UnknownToolServerDiagnostics(wf)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, `"barz"`) {
		t.Fatalf("diagnostics = %+v, want exactly one for the unknown server \"barz\"", diags)
	}
}

// A bot that names the skipped server in `mcp: servers:` is told why it is not
// there, not only that it is unknown.
func TestPrepareWorkflowExplainsASkippedServerANodeNames(t *testing.T) {
	t.Setenv(EnvAutoLoad, "true")
	dir := t.TempDir()
	writeProjectMCPFile(t, dir, `{"mcpServers":{"iterion":{"type":"stdio","command":"x"}}}`)
	wf := &ir.Workflow{MCP: &ir.MCPConfig{Servers: []string{"iterion"}}}

	skipped, err := PrepareWorkflowReport(wf, dir, dir)
	if err == nil || !strings.Contains(err.Error(), `unknown MCP server "iterion"`) || !strings.Contains(err.Error(), "skipped") || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("err = %v, want the unknown-server error with why it was skipped and how to rename it", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v, want it returned beside the error", skipped)
	}
}

func TestSkippedServerMessageNamesTheDeclaredNameWhenItDiffers(t *testing.T) {
	s, ok := reservedNameSkip("tools", &ServerConfig{Name: "iterion_tools", Origin: OriginPlugin})
	if !ok {
		t.Fatal("a reserved declared name must be skipped")
	}
	msg := s.Message()
	if !strings.Contains(msg, `"tools"`) || !strings.Contains(msg, `"iterion_tools"`) || !strings.Contains(msg, "plugin") {
		t.Errorf("message %q must name the key, the declared name and the plugin", msg)
	}
	if _, ok := reservedNameSkip("docs", &ServerConfig{Name: "docs"}); ok {
		t.Error("an ordinary server must stay")
	}
}

func TestMergeCatalogRefusesReservedPluginShape(t *testing.T) {
	for _, name := range []string{"iterion_board", "iterion_runs", "__iterion"} {
		_, err := mergeCatalog(map[string]*ServerConfig{name: {Name: name, Transport: TransportHTTP, URL: "https://untrusted.invalid/mcp"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("name %q: %v", name, err)
		}
	}
}

func TestPrepareWorkflowPreservesOrdinaryMCPNames(t *testing.T) {
	t.Setenv(EnvAutoLoad, "true")
	dir := t.TempDir()
	writeProjectMCPFile(t, dir, `{"mcpServers":{"docs":{"type":"stdio","command":"docs"},"my_iterion":{"type":"sse","url":"https://docs.invalid/sse"},"xiterion":{"type":"http","url":"https://docs.invalid/mcp"}}}`)
	wf := &ir.Workflow{MCPServers: map[string]*ir.MCPServer{"docs": {Name: "docs", Transport: ir.MCPTransportHTTP, URL: "https://override.invalid/mcp"}}}
	if err := PrepareWorkflow(wf, dir, dir); err != nil {
		t.Fatal(err)
	}
	if len(wf.ActiveMCPServers) != 3 || wf.ResolvedMCPServers["docs"].URL != "https://override.invalid/mcp" {
		t.Fatal("ordinary servers or override lost")
	}
	writeProjectMCPFile(t, dir, `{"mcpServers":{"iterion_board":{"type":"stdio","command":"untrusted"}}}`)
	t.Setenv(EnvAutoLoad, "false")
	if err := PrepareWorkflow(&ir.Workflow{}, dir, dir); err != nil {
		t.Fatalf("disabled project autoload must not read project names: %v", err)
	}
}
