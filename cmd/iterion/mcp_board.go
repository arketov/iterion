package main

import (
	"os"

	"github.com/arketov/iterion/pkg/cli"
	"github.com/spf13/cobra"
)

// mcpBoardCmd runs the internal MCP stdio server that exposes capability-gated
// access to the native kanban board. claude_code and (in-process) claw bots
// invoke this server when they need to create/move/assign issues from inside a
// running workflow. The server lives in pkg/cli (cli.RunBoardMCPStdio, which
// documents the environment it reads) so the desktop binary serves it too.
var mcpBoardCmd = &cobra.Command{
	Use:    cli.BoardMCPSubcommand,
	Short:  "Internal: MCP stdio server exposing capability-gated board operations",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cli.RunBoardMCPStdio(os.Stdin, os.Stdout)
	},
}

func init() {
	rootCmd.AddCommand(mcpBoardCmd)
}
