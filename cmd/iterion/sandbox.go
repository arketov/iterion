package main

import (
	"github.com/arketov/iterion/pkg/cli"
	"github.com/spf13/cobra"
)

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Inspect and configure the iterion sandbox subsystem",
	Long: `Inspect and configure the iterion sandbox subsystem.

The sandbox provides per-run isolation (filesystem, network, env) for
coding agents. It is opt-in via the workflow's sandbox: block or the
--sandbox CLI flag — by default iterion runs without any sandbox.

Subcommands:
  doctor   Report the active driver, runtime detection, and capabilities.
`,
}

var sandboxDoctorOpts cli.SandboxDoctorOptions

var sandboxDoctorCmd = &cobra.Command{
	Use:   "doctor [workflow.bot|workflow.botz|bundle-dir]",
	Short: "Report the active sandbox driver, capabilities, and (--strict) validate a run's config",
	Long: `Report the active sandbox driver and capabilities.

With --strict, resolve the effective sandbox spec (host + optional
workflow file + the same --sandbox/--sandbox-default-image/
--sandbox-host-state flags a run would use) and validate every config
combination BEFORE a run starts: driver availability, Docker daemon
liveness, image resolvability (registry/tag, no pull), Kubernetes
context + cloud-compatibility, host_state-vs-k8s mutual exclusion, and
network-allowlist syntax. Each failure prints an actionable remediation
hint and the command exits non-zero.

Examples:
  iterion sandbox doctor                              # host report (default)
  iterion sandbox doctor --strict                     # strict host checks
  iterion sandbox doctor --strict workflow.bot        # validate a workflow's sandbox: block
  iterion sandbox doctor --strict workflow.bot --target cloud    # validate cloud-compat from a laptop
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			sandboxDoctorOpts.File = args[0]
		}
		return cli.RunSandboxDoctor(cmd.Context(), newPrinter(), sandboxDoctorOpts)
	},
}

func init() {
	f := sandboxDoctorCmd.Flags()
	f.BoolVar(&sandboxDoctorOpts.Strict, "strict", false, "Validate every sandbox config combination and exit non-zero on any failure")
	f.StringVar(&sandboxDoctorOpts.Sandbox, "sandbox", "", "Sandbox mode override to validate (\"none\", \"auto\"); mirrors `iterion run --sandbox`")
	f.StringVar(&sandboxDoctorOpts.SandboxDefaultImage, "sandbox-default-image", "", "Image ref used by sandbox: auto when no .devcontainer/devcontainer.json is found (mirrors `iterion run`)")
	f.StringVar(&sandboxDoctorOpts.SandboxHostState, "sandbox-host-state", "", "host_state override to validate (\"auto\", \"none\"); mirrors `iterion run --sandbox-host-state`")
	f.StringVar(&sandboxDoctorOpts.Target, "target", "auto", "Check battery: auto (selected driver), cloud (kubernetes compat from any host), or local (docker)")
	sandboxCmd.AddCommand(sandboxDoctorCmd)
	rootCmd.AddCommand(sandboxCmd)
}
