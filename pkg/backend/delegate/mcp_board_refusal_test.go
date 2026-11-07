package delegate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/internal/proc"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// pinNoBoardServerBinary makes the board server resolver find nothing on any
// host: no ITERION_BIN, no install-path candidate, no self-serving.
func pinNoBoardServerBinary(t *testing.T) {
	t.Helper()
	t.Setenv("ITERION_BIN", "")
	t.Setenv("HOME", t.TempDir())
	restore := proc.SetInstallPathCandidates(func() []string { return nil })
	t.Cleanup(restore)
	proc.SetSelfServesBoardMCP(false)
}

// A node granted board.* capabilities whose stdio board server cannot be
// started used to run with the board tools allow-listed and no server behind
// them: the agent searched for the tools, found nothing and carried on
// silently (a `report` node returned posted=false with no board). It is now
// refused with the typed capability error before the CLI is spawned.
//
// Mutation seen red: drop the refusal block in ClaudeCodeBackend.Execute —
// Execute reaches the (missing) CLI and fails with another error.
func TestClaudeCodeExecute_BoardCapabilityWithoutServerBinaryIsRefused(t *testing.T) {
	pinNoBoardServerBinary(t)
	b := &ClaudeCodeBackend{
		Command: "iterion-test-no-such-claude-cli",
		Logger:  iterlog.Nop(),
	}
	_, err := b.Execute(context.Background(), Task{
		NodeID:       "report",
		Capabilities: []string{"board.read", "board.comment"},
	})
	var refused *ErrCapabilityUnsupported
	if !errors.As(err, &refused) {
		t.Fatalf("Execute error = %v, want *ErrCapabilityUnsupported", err)
	}
	if refused.Backend != BackendClaudeCode || refused.NodeID != "report" {
		t.Errorf("refusal = %+v, want backend %q for node report", refused, BackendClaudeCode)
	}
	if !strings.Contains(refused.Capability, "board tools unavailable in nodes") {
		t.Errorf("Capability = %q, want the board consequence named", refused.Capability)
	}
}

// The refusal is exactly "board capability, stdio transport, no server
// executable": a self-serving process (the desktop), a node without board.*
// capabilities and a sandboxed node (HTTP transport) are not refused.
func TestRefuseBoardWithoutServer_OnlyWhenNoExecutableCanServe(t *testing.T) {
	pinNoBoardServerBinary(t)
	task := Task{NodeID: "n", Capabilities: []string{"board.read"}}

	if err := refuseBoardWithoutServer(task, BackendClaudeCode); err == nil {
		t.Fatal("no binary resolves, want a refusal")
	}

	proc.SetSelfServesBoardMCP(true)
	t.Cleanup(func() { proc.SetSelfServesBoardMCP(false) })
	if err := refuseBoardWithoutServer(task, BackendClaudeCode); err != nil {
		t.Fatalf("a self-serving process must not be refused: %v", err)
	}
}

// The pi host branch (hostless, no HTTP endpoint) refuses the same way; with
// the HTTP pair wired, or without board.* capabilities, the node is not
// refused.
func TestPinMCPServers_BoardWithoutServerBinaryIsRefused(t *testing.T) {
	pinNoBoardServerBinary(t)

	_, cleanup, err := pinMCPServers(context.Background(), Task{
		NodeID:       "report",
		Capabilities: []string{"board.read"},
	}, "mark", nil)
	defer cleanup()
	var refused *ErrCapabilityUnsupported
	if !errors.As(err, &refused) {
		t.Fatalf("pinMCPServers error = %v, want *ErrCapabilityUnsupported", err)
	}
	if refused.Backend != BackendPi {
		t.Errorf("Backend = %q, want %q", refused.Backend, BackendPi)
	}

	path, cleanup2, err := pinMCPServers(context.Background(), Task{
		NodeID:            "report",
		Capabilities:      []string{"board.read"},
		BoardHTTPEndpoint: "http://host/api/v1/mcp/board",
		BoardRunToken:     "tok",
	}, "mark", nil)
	defer cleanup2()
	if err != nil {
		t.Fatalf("a node with the HTTP board transport must not be refused: %v", err)
	}
	if path == "" {
		t.Error("the HTTP board server must be pinned in the MCP catalog")
	}
}
