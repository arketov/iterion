package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher"
	"github.com/arketov/iterion/pkg/internal/hostshell"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// runSeedHook runs the PowerShell seed preset as the dispatcher would: cwd =
// the fresh workspace, ITERION_WORKSPACE in the environment, the hook's
// explicit `shell:`.
func runSeedHook(t *testing.T, shell, project, workspace string) string {
	t.Helper()
	h := &dispatcher.Hook{Script: workspaceSeedPowerShell(project), Shell: shell, TimeoutMS: 60_000}
	var logs bytes.Buffer
	err := h.Run(context.Background(), iterlog.New(iterlog.LevelInfo, &logs), "after_create", workspace, []string{"ITERION_WORKSPACE=" + workspace})
	if err != nil {
		t.Fatalf("seed hook under %s: %v\n%s", shell, err, logs.String())
	}
	return logs.String()
}

func gitRepoWithCommit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "tracked.txt"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// The PowerShell seed preset, run for real under each PowerShell flavour on
// PATH against a real repository: a detached worktree, the tracked file is
// there, and a second run over the now non-empty workspace is a no-op.
func TestWorkspaceSeedPowerShell_SeedsARealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	for _, shell := range []string{"pwsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not on PATH", shell)
			}
			project := filepath.Join(t.TempDir(), "proj with space")
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			gitRepoWithCommit(t, project)
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			logs := runSeedHook(t, shell, project, ws)
			if !strings.Contains(logs, "seeded git worktree") {
				t.Fatalf("no seed message: %s", logs)
			}
			if _, err := os.Stat(filepath.Join(ws, "tracked.txt")); err != nil {
				t.Fatalf("the worktree lacks the tracked file: %v", err)
			}
			again := runSeedHook(t, shell, project, ws)
			if !strings.Contains(again, "skipping seed") {
				t.Fatalf("a non-empty workspace must be left alone: %s", again)
			}
		})
	}
}

func TestWorkspaceSeedPowerShell_CopiesANonRepository(t *testing.T) {
	for _, shell := range []string{"pwsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not on PATH", shell)
			}
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "a.txt"), []byte("a"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, ".hidden"), []byte("h"), 0o600); err != nil {
				t.Fatal(err)
			}
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			if logs := runSeedHook(t, shell, project, ws); !strings.Contains(logs, "seeded copy") {
				t.Fatalf("no copy message: %s", logs)
			}
			for _, f := range []string{"a.txt", ".hidden"} {
				if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
					t.Fatalf("%s not copied: %v", f, err)
				}
			}
		})
	}
}

func TestSeedHookForHostSavesAnExplicitPowerShell(t *testing.T) {
	t.Setenv(hostshell.EnvVar, "pwsh")
	script, shell := seedHookForHost(`C:\proj`)
	if shell != "pwsh" || !strings.Contains(script, "Copy-Item") {
		t.Fatalf("PowerShell host: shell=%q script=%q", shell, script)
	}
	t.Setenv(hostshell.EnvVar, "sh")
	script, shell = seedHookForHost("/proj")
	if shell != "" || !strings.Contains(script, "set -e") {
		t.Fatalf("POSIX host: shell=%q script=%q", shell, script)
	}
}

// A config saved before the host shell followed the OS carries the sh preset
// with no `shell:`: on a PowerShell host it is rewritten into the PowerShell
// preset for the same project, with the shell saved; an edited hook, a hook
// with its own shell, and a POSIX host are left alone.
func TestMigrateLegacySeedHook(t *testing.T) {
	legacy := workspaceSeedScript(`C:\Users\Shket\SELF Repository\bars-project`)
	mk := func(script, shell string) *dispatcher.Config {
		return &dispatcher.Config{Hooks: dispatcher.Hooks{AfterCreate: &dispatcher.Hook{Script: script, Shell: shell}}}
	}

	t.Setenv(hostshell.EnvVar, "pwsh")
	cfg := mk(strings.ReplaceAll(legacy, "\n", "\r\n"), "")
	if !MigrateLegacySeedHook(cfg) {
		t.Fatal("the old sh preset must be migrated on a PowerShell host")
	}
	h := cfg.Hooks.AfterCreate
	if h.Shell != "pwsh" || !strings.Contains(h.Script, `$projectDir = 'C:\Users\Shket\SELF Repository\bars-project'`) {
		t.Fatalf("migrated hook = %+v", h)
	}
	if MigrateLegacySeedHook(cfg) {
		t.Fatal("a migrated hook must not migrate again")
	}
	if MigrateLegacySeedHook(mk(legacy+"echo extra\n", "")) {
		t.Fatal("an edited hook is the operator's: leave it")
	}
	if MigrateLegacySeedHook(mk(legacy, "bash")) {
		t.Fatal("a hook with its own shell is left alone")
	}
	t.Setenv(hostshell.EnvVar, "sh")
	if MigrateLegacySeedHook(mk(legacy, "")) {
		t.Fatal("on a POSIX host the old preset is right as it is")
	}
}

// A seed preset built on another OS (its project path is not absolute here) is
// rebuilt in the running copy for this host's shell and project; a preset for
// this host, an edited one, one with its own shell, and a missing hook or
// project are left alone.
func TestAdaptSavedConfig_ForeignPresetIsRebuiltForThisHost(t *testing.T) {
	foreign := `C:\proj`
	if hostshell.IsWindowsHost() {
		foreign = "/mnt/c/proj"
	}
	native := t.TempDir()
	mk := func(script, shell string) *dispatcher.Config {
		return &dispatcher.Config{Hooks: dispatcher.Hooks{AfterCreate: &dispatcher.Hook{Script: script, Shell: shell, TimeoutMS: 45_000}}}
	}
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	for _, hostShell := range []string{"sh", "pwsh"} {
		t.Run("rebuilt under "+hostShell, func(t *testing.T) {
			t.Setenv(hostshell.EnvVar, hostShell)
			wantScript, wantShell := seedHookForHost(native)
			for name, cfg := range map[string]*dispatcher.Config{
				"powershell preset": mk(workspaceSeedPowerShell(foreign), "pwsh"),
				"powershell crlf":   mk(crlf(workspaceSeedPowerShell(foreign)), "powershell"),
				"sh preset":         mk(workspaceSeedScript(foreign), ""),
				"sh crlf":           mk(crlf(workspaceSeedScript(foreign)), ""),
			} {
				if !AdaptSavedConfig(native)(cfg) {
					t.Fatalf("%s: a foreign preset must be adapted", name)
				}
				h := cfg.Hooks.AfterCreate
				if h.Script != wantScript || h.Shell != wantShell || h.TimeoutMS != 45_000 {
					t.Fatalf("%s: hook = %+v, want the host preset (shell %q) with the timeout kept", name, h, wantShell)
				}
			}
		})
	}

	t.Setenv(hostshell.EnvVar, "sh")
	unchanged := map[string]*dispatcher.Config{
		"native sh path":         mk(workspaceSeedScript(native), ""),
		"native powershell path": mk(workspaceSeedPowerShell(native), "pwsh"),
		"edited preset":          mk(workspaceSeedScript(foreign)+"echo extra\n", ""),
		"edited powershell":      mk(workspaceSeedPowerShell(foreign)+"Write-Output extra\n", "pwsh"),
		"own shell":              mk(workspaceSeedScript(foreign), "bash"),
		"no hook":                {},
	}
	for name, cfg := range unchanged {
		var before dispatcher.Hook
		if cfg.Hooks.AfterCreate != nil {
			before = *cfg.Hooks.AfterCreate
		}
		if AdaptSavedConfig(native)(cfg) {
			t.Errorf("%s: must be left alone", name)
		}
		if cfg.Hooks.AfterCreate != nil && *cfg.Hooks.AfterCreate != before {
			t.Errorf("%s: hook changed: %+v", name, cfg.Hooks.AfterCreate)
		}
	}
	if AdaptSavedConfig("")(mk(workspaceSeedScript(foreign), "")) {
		t.Error("with no project directory there is nothing to rebuild for")
	}
	if AdaptSavedConfig(native)(nil) {
		t.Error("a nil config is left alone")
	}
}

// The recognisers return the project path a preset was written for, for paths
// that need quoting, and reject text that is not exactly a preset.
func TestSeedPresetProjectDir(t *testing.T) {
	for _, dir := range []string{`C:\Users\O'Brien\my proj`, "/mnt/c/it's here/p", `C:\a b\c`, `/tmp/x"y`} {
		for _, eol := range []string{"\n", "\r\n"} {
			ps := strings.ReplaceAll(workspaceSeedPowerShell(dir), "\n", eol)
			if got, ok := seedPresetProjectDir(&dispatcher.Hook{Script: ps, Shell: "pwsh"}); !ok || got != dir {
				t.Errorf("powershell %q (%q): got %q, %v", dir, eol, got, ok)
			}
			sh := strings.ReplaceAll(workspaceSeedScript(dir), "\n", eol)
			if got, ok := seedPresetProjectDir(&dispatcher.Hook{Script: sh}); !ok || got != dir {
				t.Errorf("sh %q (%q): got %q, %v", dir, eol, got, ok)
			}
		}
	}
	for name, h := range map[string]*dispatcher.Hook{
		"nil":                nil,
		"foreign text":       {Script: "echo hi\n"},
		"empty project":      {Script: workspaceSeedPowerShell("")},
		"sh with a shell":    {Script: workspaceSeedScript("/p"), Shell: "pwsh"},
		"unpaired quote":     {Script: strings.Replace(workspaceSeedPowerShell("ab"), "'ab'", "'a'b'", 1)},
		"powershell, bash":   {Script: workspaceSeedPowerShell("C:/p"), Shell: "bash"},
		"powershell + extra": {Script: workspaceSeedPowerShell("C:/p") + "x", Shell: "pwsh"},
	} {
		if dir, ok := seedPresetProjectDir(h); ok {
			t.Errorf("%s: recognised as a preset for %q", name, dir)
		}
	}
}

// The two presets are told apart by hostshell's sniffing: the sh one is sh,
// the PowerShell one never is, so a saved PowerShell preset is not mistaken
// for legacy sh text.
func TestSeedPresetsAreToldApart(t *testing.T) {
	if !hostshell.LooksPosix(workspaceSeedScript("/proj")) {
		t.Error("the sh seed preset must read as sh")
	}
	if hostshell.LooksPosix(workspaceSeedPowerShell("C:/proj")) {
		t.Error("the PowerShell seed preset must not read as sh")
	}
}
