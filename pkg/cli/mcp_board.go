package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/native/boardops"
	"github.com/arketov/iterion/pkg/internal/proc"
)

// BoardMCPSubcommand is the hidden argv[1] that turns an iterion executable
// into the internal MCP stdio server exposing capability-gated board
// operations. claude_code and pi nodes holding board.* capabilities spawn it.
const BoardMCPSubcommand = "__mcp-board"

// RunBoardMCPStdio runs the board MCP stdio server over in/out.
//
// Configuration via environment:
//   - ITERION_STORE_DIR : path to the dispatcher store root. If unset, falls
//     back to ./.iterion/dispatcher relative to the current working directory.
//   - ITERION_BOARD_CAPS: comma-separated list of granted capabilities.
//     Empty/unset = no tools exposed (tools/list returns []).
//   - ITERION_SOURCE_ISSUE_ID: planner provenance; create_issue auto-sets
//     parent_id from it when the agent omits it.
func RunBoardMCPStdio(in io.Reader, out io.Writer) error {
	store, err := openBoardStoreFromEnv()
	if err != nil {
		return err
	}
	caps := boardops.NewCapabilities(os.Getenv("ITERION_BOARD_CAPS"))
	env := boardops.CallEnv{SpawnParentID: strings.TrimSpace(os.Getenv("ITERION_SOURCE_ISSUE_ID"))}
	return runMCPBoardServer(in, out, store, caps, env)
}

// MaybeServeBoardMCP serves the board MCP stdio server when args (os.Args[1:])
// is exactly the hidden `__mcp-board` subcommand and reports handled=true. For
// any other args it does nothing. The desktop binary calls it first thing in
// main(): it has no cobra root, and stdout is the JSON-RPC channel.
func MaybeServeBoardMCP(args []string, in io.Reader, out io.Writer) (handled bool, err error) {
	if len(args) != 1 || args[0] != BoardMCPSubcommand {
		return false, nil
	}
	return true, RunBoardMCPStdio(in, out)
}

// DeclareSelfServesBoardMCP records that this process dispatches
// BoardMCPSubcommand (see MaybeServeBoardMCP), so a node needing the board
// server can spawn the process's own executable.
func DeclareSelfServesBoardMCP() {
	proc.SetSelfServesBoardMCP(true)
}

// openBoardStoreFromEnv resolves the dispatcher store root and opens it.
// native.NewStore owns directory creation (it MkdirAlls root + issues/
// itself), so a standalone `iterion run` hitting a fresh workspace
// gets a board lazily without any prior `iterion issue board init`.
func openBoardStoreFromEnv() (*native.Store, error) {
	root := os.Getenv("ITERION_STORE_DIR")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve cwd: %w", err)
		}
		root = filepath.Join(cwd, ".iterion", "dispatcher")
	}
	return native.NewStore(root)
}

func runMCPBoardServer(in io.Reader, out io.Writer, store *native.Store, caps boardops.Capabilities, env boardops.CallEnv) error {
	return RunMCPLoop(in, out, 1024*1024, func(req MCPRequest) MCPResponse {
		return dispatchMCPBoard(req, store, caps, env)
	})
}

func dispatchMCPBoard(req MCPRequest, store *native.Store, caps boardops.Capabilities, env boardops.CallEnv) MCPResponse {
	resp := MCPResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = MCPInitializeResult("iterion-board")
	case "tools/list":
		tools := boardops.ToolsFor(caps)
		entries := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			entries = append(entries, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		resp.Result = map[string]any{"tools": entries}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = MCPInvalidParamsError(err)
			return resp
		}
		raw, err := boardops.CallWithEnv(store, caps, params.Name, params.Arguments, env)
		if err != nil {
			if errors.Is(err, boardops.ErrCapabilityDenied) {
				resp.Error = &MCPError{Code: -32601, Message: err.Error()}
				return resp
			}
			// Other errors (unknown state, validation, etc) are returned as
			// tool errors (isError:true) so the LLM can recover.
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}
			return resp
		}
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(raw)}},
			"isError": false,
		}
	default:
		resp.Error = MCPMethodNotFoundError(req.Method)
	}
	return resp
}
