package runview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/backend/mcp"
)

// TestLaunchRunLogShowsAReservedMCPServerSkip pins the studio half of the
// reserved-name skip: the compile leaves a project `.mcp.json` entry named
// under iterion's reserved namespace out of the catalog and reports it on the
// CompiledSource, and the run's own log — the one a launch or resume prepares
// after the compile — carries the same `mcp: ...` line the runner logs. It was
// only visible in `validate`; the studio's compile had no logger to say it on.
func TestLaunchRunLogShowsAReservedMCPServerSkip(t *testing.T) {
	t.Setenv(mcp.EnvAutoLoad, "true")
	dir := t.TempDir()
	const src = `prompt sys:
  Hi.
prompt usr:
  go.
schema out:
  x: string
agent a:
  backend: "claw"
  model: "openai/gpt-5.4-mini"
  system: sys
  user: usr
  output: out
  session: fresh
workflow main:
  entry: a
  a -> done
`
	bot := filepath.Join(dir, "main.bot")
	if err := os.WriteFile(bot, []byte(src), 0o644); err != nil {
		t.Fatalf("write bot: %v", err)
	}
	const mcpJSON = `{"mcpServers":{"iterion_stray":{"type":"stdio","command":"untrusted"},"docs":{"type":"stdio","command":"docs"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}

	_, cs, _, err := compileForLaunch(bot, "", "", "", dir)
	if err != nil {
		t.Fatalf("compileForLaunch: %v", err)
	}
	if len(cs.MCPSkipped) != 1 || cs.MCPSkipped[0].Name != "iterion_stray" {
		t.Fatalf("CompiledSource.MCPSkipped = %+v, want the one reserved-name entry", cs.MCPSkipped)
	}

	svc, err := NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	buf, logger := svc.prepareRunLog("run-mcp-skip")
	logMCPSkips(logger, cs)

	_, data, _ := buf.Snapshot(0)
	got := string(data)
	want := `mcp: MCP server "iterion_stray" from .mcp.json is skipped`
	if !strings.Contains(got, want) {
		t.Fatalf("run log lacks %q:\n%s", want, got)
	}
}
