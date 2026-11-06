package proc

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

// selfServesBoardMCP is set by an executable that dispatches the hidden
// `__mcp-board` subcommand itself (the desktop binary).
var selfServesBoardMCP atomic.Bool

// SetSelfServesBoardMCP declares whether this process's own executable serves
// the `__mcp-board` subcommand. Only the desktop binary sets it: it does not
// dispatch the other hidden subcommands, which is why LocateBoardMCPBinary is
// separate from LocateIterionBinary.
func SetSelfServesBoardMCP(v bool) { selfServesBoardMCP.Store(v) }

// SelfServesBoardMCP reports whether SetSelfServesBoardMCP(true) was called.
func SelfServesBoardMCP() bool { return selfServesBoardMCP.Load() }

// LocateBoardMCPBinary returns the absolute path of an executable able to run
// `__mcp-board`, or "" when none exists. An iterion CLI found by
// LocateIterionBinary (explicit ITERION_BIN, sibling, install paths) wins;
// otherwise the process's own executable is used when it declared that it
// serves the board MCP server itself, so the desktop works with no separate
// iterion binary next to it.
//
// The executable-bit check is skipped for the self path: the process is by
// definition running from that file.
func LocateBoardMCPBinary() string {
	if p := LocateIterionBinary(); p != "" {
		return p
	}
	if !selfServesBoardMCP.Load() {
		return ""
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return ""
	}
	return abs
}
