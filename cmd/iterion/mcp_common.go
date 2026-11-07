package main

import (
	"io"

	"github.com/arketov/iterion/pkg/cli"
)

// The JSON-RPC 2.0 plumbing shared by the internal MCP stdio servers
// (__mcp-ask-user, __mcp-board, __mcp-control) lives in pkg/cli so the
// desktop binary can serve __mcp-board too; these aliases keep the
// servers in this package unchanged.

type mcpRequest = cli.MCPRequest
type mcpResponse = cli.MCPResponse
type mcpError = cli.MCPError

func mcpInitializeResult(serverName string) any { return cli.MCPInitializeResult(serverName) }

func mcpMethodNotFoundError(method string) *mcpError { return cli.MCPMethodNotFoundError(method) }

func mcpInvalidParamsError(err error) *mcpError { return cli.MCPInvalidParamsError(err) }

func runMCPLoop(in io.Reader, out io.Writer, bufMax int, dispatch func(req mcpRequest) mcpResponse) error {
	return cli.RunMCPLoop(in, out, bufMax, dispatch)
}
