package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Shared JSON-RPC 2.0 message types for the internal MCP stdio servers
// (__mcp-ask-user, __mcp-board, __mcp-control). All three speak the same
// line-delimited JSON-RPC wire format, so they share these structs. They live
// in pkg/cli (not cmd/iterion) so the desktop binary can serve __mcp-board
// itself.

// MCPRequest is one inbound JSON-RPC request line.
type MCPRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// MCPResponse is one outbound JSON-RPC response line.
type MCPResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *MCPError        `json:"error,omitempty"`
}

// MCPError is the JSON-RPC error object.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPInitializeResult builds the "initialize" response result shared by
// every internal MCP stdio server; only the advertised server name varies.
func MCPInitializeResult(serverName string) any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": Version(),
		},
	}
}

// MCPMethodNotFoundError builds the -32601 error for an unhandled method.
func MCPMethodNotFoundError(method string) *MCPError {
	return &MCPError{Code: -32601, Message: fmt.Sprintf("method not found: %s", method)}
}

// MCPInvalidParamsError builds the -32602 error for a params-unmarshal failure.
func MCPInvalidParamsError(err error) *MCPError {
	return &MCPError{Code: -32602, Message: fmt.Sprintf("invalid params: %s", err)}
}

// RunMCPLoop drives a line-delimited JSON-RPC server over the given streams.
// For each non-empty input line it:
//   - replies with a -32700 parse error (id=null) on json.Unmarshal failure;
//   - drops notifications (req.ID == nil) silently;
//   - otherwise invokes dispatch(req) and writes the returned MCPResponse.
//
// Returns nil on clean EOF. bufMax sizes the scanner's read buffer (MCP
// messages can exceed bufio's 64KB default; callers pick the cap that fits
// their largest expected payload).
func RunMCPLoop(in io.Reader, out io.Writer, bufMax int, dispatch func(req MCPRequest) MCPResponse) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), bufMax)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req MCPRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(MCPResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &MCPError{Code: -32700, Message: fmt.Sprintf("parse error: %s", err)},
			})
			continue
		}
		if req.ID == nil {
			continue // notification — no response
		}
		resp := dispatch(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		// A single line beyond bufMax poisons the scanner (it cannot
		// resync mid-line), so the server has to exit — but the client
		// deserves to know WHY instead of watching the process vanish.
		if errors.Is(err, bufio.ErrTooLong) {
			_ = enc.Encode(MCPResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &MCPError{Code: -32700, Message: fmt.Sprintf("request line exceeds the %d-byte limit; the server cannot recover and will exit", bufMax)},
			})
		}
		return err
	}
	return nil
}
