package main

import (
	"github.com/arketov/iterion/pkg/cli"
	"github.com/spf13/cobra"
)

var superviseOpts cli.SuperviseOptions

var superviseCmd = &cobra.Command{
	Use:   "supervise --run-id <id> [flags]",
	Short: "Attach an LLM supervisor to a running run",
	Long: `Attach an LLM supervisor agent to an already-running run. The
supervisor watches the run's live event stream and enqueues steering
messages the supervised agent picks up at its NEXT turn — like a human
operator watching a Claude Code session and typing a correction.

Scope it to specific agent nodes with --node (repeatable); the
supervisor is only armed while one of those nodes is the active node and
its messages are node-scoped so a late message can't leak into the next
node. Omit --node to watch the whole run.

The supervisor is event-driven: give it a --system policy describing what
to watch for, and optionally pre-declare --monitor patterns (it can also
register more at runtime). It only consults the LLM on turn boundaries
(rate-limited by --cooldown) and on monitor matches.

A run whose .bot declares a 'supervisor' block auto-spawns the same
coordinator and needs none of this.

Examples:
  iterion supervise --run-id run-123 \
    --node implement \
    --system @policies/watchdog.md \
    --monitor event_type=tool_error,tool_name=Bash

  iterion supervise --run-id run-123 --model anthropic/claude-opus-4-8 \
    --system "Stop the agent if it edits files outside src/."
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.RunSupervise(newPrinter(), superviseOpts)
	},
}

var (
	superviseHookProject bool
	superviseHookDir     string
)

var superviseInstallHookCmd = &cobra.Command{
	Use:   "install-hook",
	Short: "Install the drain hook into a repo's Claude Code settings",
	Long: `Install the iterion drain hook into a target repo's Claude Code
settings (.claude/settings.local.json by default) so that messages a
supervisor enqueues for a raw Claude Code session in that repo are
delivered at the session's next tool/stop boundary. Non-destructive and
idempotent.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.RunSuperviseInstallHook(newPrinter(), superviseHookDir, superviseHookProject, false)
	},
}

var superviseUninstallHookCmd = &cobra.Command{
	Use:   "uninstall-hook",
	Short: "Remove the drain hook from a repo's Claude Code settings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.RunSuperviseInstallHook(newPrinter(), superviseHookDir, superviseHookProject, true)
	},
}

func init() {
	f := superviseCmd.Flags()
	f.StringVar(&superviseOpts.RunID, "run-id", "", "ID of the iterion run to supervise")
	f.StringVar(&superviseOpts.Session, "claude-session", "", "Supervise a raw Claude Code session: its cwd (directory) or session id")
	f.StringVar(&superviseOpts.Name, "name", "", "Supervisor name (shown in injected messages and logs)")
	f.StringVar(&superviseOpts.Model, "model", "", "Supervisor model spec, e.g. anthropic/claude-opus-4-8 (default: auto-detect / ITERION_DEFAULT_SUPERVISOR_MODEL)")
	f.StringVar(&superviseOpts.System, "system", "", "Supervision policy text, or @path to read it from a file")
	f.StringSliceVar(&superviseOpts.Nodes, "node", nil, "Agent node id(s) to watch (repeatable; empty = whole run)")
	f.StringArrayVar(&superviseOpts.Monitors, "monitor", nil, "Pre-declared monitor as key=val,key=val (repeatable). Keys: event_type,node_id,tool_name,text_contains,cost_gt")
	f.DurationVar(&superviseOpts.Cooldown, "cooldown", 0, "Minimum time between LLM evaluations on turn boundaries (default 30s)")
	f.IntVar(&superviseOpts.MaxEvals, "max-evals", 0, "Hard cap on LLM evaluations for the run (default 20)")
	f.StringVar(&superviseOpts.StoreDir, "store-dir", "", "Override the iterion store directory")

	for _, hc := range []*cobra.Command{superviseInstallHookCmd, superviseUninstallHookCmd} {
		hc.Flags().StringVar(&superviseHookDir, "cwd", "", "Target repo directory (default: current directory)")
		hc.Flags().BoolVar(&superviseHookProject, "project", false, "Write to .claude/settings.json (shared) instead of settings.local.json")
	}
	superviseCmd.AddCommand(superviseInstallHookCmd, superviseUninstallHookCmd)
	rootCmd.AddCommand(superviseCmd)
}
