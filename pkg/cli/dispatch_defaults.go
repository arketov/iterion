package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arketov/iterion/pkg/botregistry"
	"github.com/arketov/iterion/pkg/dispatcher"
	"github.com/arketov/iterion/pkg/internal/hostshell"
)

// defaultDispatchPort is the studio HTTP surface for the no-arg mode.
// 4892 matches the example in docs/dispatcher.md so the published port
// stays stable across iterations.
const defaultDispatchPort = 4892

// defaultDispatchMaxConcurrent is intentionally conservative — the
// zero-config mode is meant for laptop / single-developer use, not
// fleet operation. Operators who need more should write a YAML.
const defaultDispatchMaxConcurrent = 2

// legacyDefaultPresetName is the Name the zero-config preset has always
// carried; MigrateLegacyDefaultPreset keys on it.
const legacyDefaultPresetName = "iterion-default"

// workspaceSeedScript is the after_create hook that seeds a fresh issue
// workspace from the host repository: a detached `git worktree add` when the
// project is one, a recursive copy otherwise. The worktree add runs with the
// host repository's hooks off — its hooks directory and config are a run's
// to write, and the seeding is the dispatcher's gesture, not the run's.
func workspaceSeedScript(projectDir string) string {
	return fmt.Sprintf(`set -e
PROJECT_DIR=%q
# If the workspace already has content (e.g. a previous failed
# attempt populated it before crashing), don't re-seed — let the
# operator clean up manually rather than silently overwrite.
if [ "$(ls -A "$ITERION_WORKSPACE" 2>/dev/null | head -c1)" != "" ]; then
  echo "workspace $ITERION_WORKSPACE non-empty — skipping seed"
  exit 0
fi
# Prefer a git worktree (cheap, shares object store with the host
# repo, isolates branches). Fall back to a recursive copy when
# PROJECT_DIR is not a git repository so out-of-tree projects
# still work.
if git -C "$PROJECT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
  # Use detached HEAD so two parallel issues don't fight over the
  # same branch name. Bots that need a branch can create one with
  # workflow-level worktree: auto on top. The hooks directory and
  # config of the host repository are a run's to write: none runs
  # while the workspace is seeded from it.
  git -C "$PROJECT_DIR" -c core.hooksPath=/dev/null -c core.fsmonitor=false worktree add --detach "$ITERION_WORKSPACE" HEAD
  echo "seeded git worktree from $PROJECT_DIR@HEAD"
else
  cp -a "$PROJECT_DIR/." "$ITERION_WORKSPACE/"
  echo "seeded copy from $PROJECT_DIR"
fi
`, projectDir)
}

// workspaceSeedPowerShell is workspaceSeedScript for a PowerShell host (the
// Windows default): the same decisions — skip a non-empty workspace, a
// detached `git worktree add` when the project is a repository, a recursive
// copy otherwise — in PowerShell, with the same hooks-off worktree add. It
// refuses to run without ITERION_WORKSPACE: an empty $ws drops out of the argv
// of `git worktree add` (PowerShell 5.1) and git would create a worktree named
// HEAD inside the host project.
func workspaceSeedPowerShell(projectDir string) string {
	q := "'" + strings.ReplaceAll(projectDir, "'", "''") + "'"
	return `$projectDir = ` + q + `
$ws = $env:ITERION_WORKSPACE
if ([string]::IsNullOrWhiteSpace($ws)) {
  [Console]::Error.WriteLine('ITERION_WORKSPACE is not set - the workspace seed needs the workspace path from the dispatcher')
  exit 2
}
# A workspace that already has content (a previous failed attempt) is not
# re-seeded: the operator cleans it up rather than being silently overwritten.
if ((Test-Path -LiteralPath $ws) -and (Get-ChildItem -LiteralPath $ws -Force | Select-Object -First 1)) {
  Write-Output "workspace $ws non-empty - skipping seed"
  exit 0
}
git -C $projectDir rev-parse --git-dir *> $null
if ($LASTEXITCODE -eq 0) {
  # Detached HEAD, the host repository's hooks and fsmonitor off: see
  # workspaceSeedScript.
  git -C $projectDir -c core.hooksPath=/dev/null -c core.fsmonitor=false worktree add --detach $ws HEAD
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  Write-Output "seeded git worktree from ${projectDir}@HEAD"
} else {
  if (-not (Test-Path -LiteralPath $ws)) { New-Item -ItemType Directory -Path $ws | Out-Null }
  Get-ChildItem -LiteralPath $projectDir -Force | Copy-Item -Destination $ws -Recurse -Force
  Write-Output "seeded copy from $projectDir"
}
`
}

// seedHookForHost picks the seed script in the dialect of the shell the
// after_create hook will run under (hostshell: PowerShell on Windows, sh
// elsewhere, or what ITERION_SHELL names), and, when that is PowerShell,
// the `shell:` to save with it: the dialect and the shell travel together in
// the persisted config, so a later change of the host default cannot hand a
// PowerShell script to sh or the reverse. On POSIX shell stays empty (the
// platform default is the dialect).
func seedHookForHost(projectDir string) (script, shell string) {
	sh, err := hostshell.Resolve(hostshell.Request{})
	powershell := sh.Kind == hostshell.KindPowerShell
	if err != nil {
		// No shell found: the hook reports it when it runs; the script is the
		// one the platform default would have used.
		powershell = hostshell.IsWindowsHost()
	}
	if !powershell {
		return workspaceSeedScript(projectDir), ""
	}
	if err != nil {
		// No PowerShell found: nothing to name. The preset is still the
		// platform's, and the hook reports the missing shell when it runs.
		return workspaceSeedPowerShell(projectDir), ""
	}
	shell = "powershell"
	if base := strings.ToLower(strings.TrimSuffix(filepath.Base(sh.Path), ".exe")); base == "pwsh" {
		shell = "pwsh"
	}
	return workspaceSeedPowerShell(projectDir), shell
}

// legacySeedProjectDir reports whether script is the sh seed preset
// (workspaceSeedScript) a config saved before the host shell followed the OS
// carries, and for which project directory it was written.
func legacySeedProjectDir(script string) (string, bool) {
	script = strings.ReplaceAll(script, "\r\n", "\n")
	pre, post, ok := strings.Cut(workspaceSeedScript(""), `PROJECT_DIR=""`)
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(script, pre+"PROJECT_DIR=") || !strings.HasSuffix(script, post) {
		return "", false
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(script, pre+"PROJECT_DIR="), post)
	dir, err := strconv.Unquote(quoted)
	if err != nil || dir == "" {
		return "", false
	}
	return dir, true
}

// MigrateLegacySeedHook rewrites, in place, a saved dispatcher config whose
// after_create hook is the old sh seed preset with no `shell:` into the preset
// for this host's shell, with `shell:` saved explicitly. It changes nothing
// where the host shell is a POSIX one (the old text is right there), and
// leaves any hook the operator wrote or edited alone — those are handled when
// they run (hostshell.Request.Script). Reports whether cfg changed.
func MigrateLegacySeedHook(cfg *dispatcher.Config) bool {
	if cfg == nil || cfg.Hooks.AfterCreate == nil || cfg.Hooks.AfterCreate.Shell != "" {
		return false
	}
	dir, ok := legacySeedProjectDir(cfg.Hooks.AfterCreate.Script)
	if !ok {
		return false
	}
	script, shell := seedHookForHost(dir)
	if shell == "" {
		return false
	}
	cfg.Hooks.AfterCreate.Script = script
	cfg.Hooks.AfterCreate.Shell = shell
	return true
}

// legacyBotsDir is where an earlier version of the zero-config preset
// extracted its embedded bot catalogue: <storeDir>/dispatcher/bots.
func legacyBotsDir(storeDir string) string {
	return filepath.Join(storeDir, "dispatcher", "bots")
}

// MigrateLegacyDefaultPreset rewrites, in place, a saved dispatcher config that
// is the zero-config preset of an earlier version — one that routed to the bots
// the binary embedded (extracted under <storeDir>/dispatcher/bots, with the
// paid `default` bot as the fallback workflow and a snapshot of those bots'
// dispatch vars) — into the preset that runs only the project's own bots:
// no fallback workflow, that extracted catalogue off bots.paths, the dispatch
// vars snapshot dropped (the bots' manifests are read live now), require_assignee
// on. A config the operator wrote or changed (a different name, or a workflow
// that is not the extracted default) is left alone. The extracted files stay on
// disk — they may carry the operator's edits — they are just no longer wired.
// Reports whether cfg changed.
func MigrateLegacyDefaultPreset(cfg *dispatcher.Config, storeDir string) bool {
	if cfg == nil || storeDir == "" || cfg.Name != legacyDefaultPresetName {
		return false
	}
	botsDir := legacyBotsDir(storeDir)
	if filepath.Clean(cfg.Workflow) != filepath.Join(botsDir, "default") {
		return false
	}
	cfg.Workflow = ""
	cfg.RequireAssignee = true
	cfg.AssigneeDispatch = nil
	kept := cfg.Bots.Paths[:0:0]
	for _, p := range cfg.Bots.Paths {
		if filepath.Clean(p) != filepath.Clean(botsDir) {
			kept = append(kept, p)
		}
	}
	cfg.Bots.Paths = kept
	return true
}

// MigrateSavedConfig is the dispatcher.Manager MigrateConfig hook for the store
// at storeDir: it brings a config saved by an earlier version up to date — the
// zero-config preset that routed to embedded bots, and the sh seed hook on a
// PowerShell host. Reports whether cfg changed.
func MigrateSavedConfig(storeDir string) func(*dispatcher.Config) bool {
	return func(cfg *dispatcher.Config) bool {
		preset := MigrateLegacyDefaultPreset(cfg, storeDir)
		hook := MigrateLegacySeedHook(cfg)
		return preset || hook
	}
}

// BuildDefaultConfig returns the in-memory dispatcher configuration
// used by `iterion dispatch` when invoked without a YAML argument and by the
// studio's "Use default settings": native tracker, HTTP on
// [defaultDispatchPort], polling every 30 s, and the PROJECT's own bots
// (<projectDir>/bots, examples, .botz — botregistry.DefaultPaths, the same roots
// the studio's Bots page lists) as the assignees a card can be given.
//
// There is no built-in bot and no fallback workflow: a card is run only by a
// bot of the project it is assigned to. A project with no bots yet gets a
// working dispatcher whose cards wait, flagged "no assignee" / "no bot", until
// a bot is added (the roots are read at every dispatch, so a bot created later
// is picked up without touching the settings). Each bot's input variables come
// from its manifest `dispatch_vars`, read at dispatch time.
//
// projectDir is the host git repository the dispatcher seeds per-issue
// workspaces from and the root of the bot paths. It is required: the preset has
// nowhere to look for bots without it. The after_create hook runs `git
// worktree add` from projectDir into the freshly-created issue workspace, so
// bots see a populated checkout matching the host repo's HEAD instead of an
// empty directory.
//
// The returned Config has [Config.ApplyDefaults] already applied and
// has passed Validate, so callers can hand it straight to a Manager.
// SourcePath is left empty so downstream code (notably the
// ConfigWatcher) can tell baked-in mode apart from YAML-on-disk mode.
func BuildDefaultConfig(projectDir string) (*dispatcher.Config, error) {
	if projectDir == "" {
		return nil, errors.New("dispatch defaults: project directory is required — the preset runs the bots of a project")
	}
	// Resolve the project dir to an absolute path so the hook and the bot paths
	// are unaffected by the dispatcher daemon's cwd at the time they are used
	// (workers cd into the freshly-created workspace before invoking the hook,
	// so a relative path would be relative to the empty workspace — wrong
	// source).
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("dispatch defaults: resolve project directory %s: %w", projectDir, err)
	}
	projectDir = absProject

	var hooks dispatcher.Hooks
	// after_create runs once per workspace, the first time the dispatcher
	// claims an issue. The dispatcher passes ITERION_WORKSPACE in the env and
	// sets cwd to it; the shell hook seeds it as a git worktree of
	// projectDir@HEAD (or copies the tree when projectDir is not a git repo) so
	// the bot's workspace_dir input lands on a real checkout matching the host
	// state at dispatch time.
	seedScript, seedShell := seedHookForHost(projectDir)
	hooks.AfterCreate = &dispatcher.Hook{
		Script:    seedScript,
		Shell:     seedShell,
		TimeoutMS: 120_000,
	}
	// No destructive before_remove default: after removing an owned,
	// clean workspace the dispatcher deregisters that exact linked
	// worktree through the surviving host-repository metadata. A
	// shell-level `git worktree remove --force` before deletion would
	// reopen a commit-vs-remove race.

	cfg := &dispatcher.Config{
		Name: legacyDefaultPresetName,
		// No fallback workflow: a card that routes to no bot does not run (see
		// RequireAssignee), it waits flagged "no assignee" / "no bot".
		//
		// A card dragged to ready with no assignee must not silently start a
		// paid run: it waits until someone picks a bot. The same holds for an
		// assignee that is not a bot of the project (a person's name, a typo).
		RequireAssignee: true,
		Tracker:         dispatcher.TrackerConfig{Kind: dispatcher.TrackerKindNative},
		Polling:         dispatcher.PollingConfig{IntervalMS: 30_000},
		Agent: dispatcher.AgentConfig{
			MaxConcurrent: defaultDispatchMaxConcurrent,
			// Move claimed issues to `in_progress` so the kanban
			// surfaces in-flight work. Matches native.StateInProgress;
			// duplicated as a literal to avoid importing native here
			// (Config.ApplyDefaults would also fill this in, but
			// stating it explicitly keeps the zero-config contract
			// obvious at the call site).
			RunningState: dispatcher.DefaultRunningState,
		},
		Server: dispatcher.ServerConfig{Port: defaultDispatchPort},
		// Discovery-driven routing: RoutingRunner resolves any ENABLED
		// discovered bot by name against Bots.Paths (HasRoute consults the
		// registry), so there is no assignee_workflows map and no
		// assignee_dispatch snapshot — every bot of the project routes
		// identically and brings its own dispatch_vars. The roots are the
		// canonical botregistry.DefaultPaths (bots/ + examples/ + .botz): the
		// SAME set studio/server discover, so the CLI dispatcher and studio see
		// one catalog. Missing roots are skipped by discovery.
		Bots: botregistry.Config{Paths: botregistry.DefaultPaths(projectDir)},
		Dispatch: dispatcher.DispatchConfig{
			Vars: map[string]string{
				"issue_title": "{{issue.title}}",
				"issue_body":  "{{issue.body}}",
				"issue_id":    "{{issue.identifier}}",
			},
		},
		Hooks: hooks,
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("dispatch defaults: validate built-in config: %w", err)
	}
	return cfg, nil
}

// DefaultAssigneeNames returns the enabled bots discovered under paths,
// by name, sorted — the routable assignees for the CLI startup banner.
// Discovery-driven (no hardcoded list), so it reflects custom bots too.
func DefaultAssigneeNames(paths []string) []string {
	entries, err := botregistry.List(botregistry.ListOptions{Paths: paths})
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Enabled {
			continue
		}
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}
