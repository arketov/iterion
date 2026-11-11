package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// A project on a deep path makes `git worktree add` fail on Windows ("'$GIT_DIR'
// too big"): git's own message names no remedy, so the preset adds one, and the
// hook still exits with git's code. Windows-only: elsewhere git has no such limit.
func TestWorkspaceSeedPowerShell_AdvisesMovingAProjectWhoseGitdirIsTooLong(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the gitdir length limit is git on Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	for _, shell := range []string{"pwsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not on PATH", shell)
			}
			project := t.TempDir()
			for len(project) < 170 {
				project = filepath.Join(project, "deep-project-folder")
			}
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			gitRepoWithCommit(t, project)
			// <project>\.git\worktrees\<42-char name> is over 220 bytes.
			ws := filepath.Join(t.TempDir(), strings.Repeat("w", 42))
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			h := &dispatcher.Hook{Script: workspaceSeedPowerShell(project), Shell: shell, TimeoutMS: 60_000}
			var logs bytes.Buffer
			err := h.Run(context.Background(), iterlog.New(iterlog.LevelInfo, &logs), "after_create", ws, []string{"ITERION_WORKSPACE=" + ws})
			var he *dispatcher.HookError
			if !errors.As(err, &he) {
				t.Fatalf("a too-long gitdir must fail the seed hook, got %v\n%s", err, logs.String())
			}
			if he.ExitCode == 0 {
				t.Fatalf("the hook must keep git's non-zero exit code: %+v", he)
			}
			if !strings.Contains(he.Stderr, "move the project") {
				t.Fatalf("the hook's stderr lacks the advice to move the project: %q", he.Stderr)
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

// Without ITERION_WORKSPACE an empty $ws vanishes from the argv of `git
// worktree add` (Windows PowerShell 5.1), so git creates a worktree named HEAD
// inside the host project and the hook exits 0. The preset refuses instead:
// exit 2, the variable named on stderr, nothing created.
func TestWorkspaceSeedPowerShell_RefusesWithoutAWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	for _, shell := range []string{"pwsh", "powershell"} {
		for _, isRepo := range []bool{true, false} {
			name := shell + "/repository"
			if !isRepo {
				name = shell + "/non-repository"
			}
			t.Run(name, func(t *testing.T) {
				if _, err := exec.LookPath(shell); err != nil {
					t.Skipf("%s is not on PATH", shell)
				}
				project := t.TempDir()
				if isRepo {
					gitRepoWithCommit(t, project)
				} else if err := os.WriteFile(filepath.Join(project, "a.txt"), []byte("a"), 0o600); err != nil {
					t.Fatal(err)
				}
				cwd := t.TempDir()
				h := &dispatcher.Hook{Script: workspaceSeedPowerShell(project), Shell: shell, TimeoutMS: 60_000}
				var logs bytes.Buffer
				err := h.Run(context.Background(), iterlog.New(iterlog.LevelInfo, &logs), "after_create", cwd, []string{"ITERION_WORKSPACE="})
				var he *dispatcher.HookError
				if !errors.As(err, &he) {
					t.Fatalf("an empty ITERION_WORKSPACE must fail the hook, got %v\n%s", err, logs.String())
				}
				if he.ExitCode == 0 || !strings.Contains(he.Stderr, "ITERION_WORKSPACE") {
					t.Fatalf("exit=%d stderr=%q: want a non-zero exit naming ITERION_WORKSPACE", he.ExitCode, he.Stderr)
				}
				if _, err := os.Stat(filepath.Join(project, "HEAD")); err == nil {
					t.Fatal("a stray HEAD was created inside the host project")
				}
				if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
					t.Fatalf("the hook working directory must stay empty: %v %v", entries, err)
				}
				if isRepo {
					out, err := exec.Command("git", "-C", project, "worktree", "list").CombinedOutput()
					if err != nil {
						t.Fatalf("git worktree list: %v\n%s", err, out)
					}
					if n := len(strings.Split(strings.TrimSpace(string(out)), "\n")); n != 1 {
						t.Fatalf("a worktree was registered:\n%s", out)
					}
				}
			})
		}
	}
}

// The seeding is the dispatcher's gesture: the host repository's post-checkout
// hook must not run during the worktree add (core.hooksPath=/dev/null).
func TestWorkspaceSeedPowerShell_RunsNoHostRepositoryHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	for _, shell := range []string{"pwsh", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s is not on PATH", shell)
			}
			project := t.TempDir()
			gitRepoWithCommit(t, project)
			marker := filepath.Join(t.TempDir(), "hook-ran")
			hook := "#!/bin/sh\necho ran >> \"" + filepath.ToSlash(marker) + "\"\n"
			if err := os.WriteFile(filepath.Join(project, ".git", "hooks", "post-checkout"), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			// Control: without the override this git does run the hook.
			ctrl := filepath.Join(t.TempDir(), "ctrl")
			if out, err := exec.Command("git", "-C", project, "worktree", "add", "--detach", ctrl, "HEAD").CombinedOutput(); err != nil {
				t.Fatalf("control worktree add: %v\n%s", err, out)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Skip("this git does not run post-checkout here")
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			runSeedHook(t, shell, project, ws)
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the host repository's post-checkout hook ran while seeding")
			}
			if _, err := os.Stat(filepath.Join(ws, "tracked.txt")); err != nil {
				t.Fatalf("the worktree lacks the tracked file: %v", err)
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

// The shortened sh variant of the old preset (set -e, PROJECT_DIR, the worktree
// add, no guard or copy fallback) is migrated like the preset itself, so a
// Windows host without sh is not left with a hook it cannot run; anything beyond
// those three lines, or another worktree add, is the operator's and stays.
func TestMigrateLegacySeedHook_ShortenedVariant(t *testing.T) {
	dir := `C:\Users\Shket\SELF Repository\bars-project`
	short := func(add string) string {
		return fmt.Sprintf("set -e\nPROJECT_DIR=%q\n%s\n", dir, add)
	}
	const add = `git -C "$PROJECT_DIR" -c core.hooksPath=/dev/null worktree add --detach "$ITERION_WORKSPACE" HEAD`
	mk := func(script string) *dispatcher.Config {
		return &dispatcher.Config{Hooks: dispatcher.Hooks{AfterCreate: &dispatcher.Hook{Script: script}}}
	}

	t.Setenv(hostshell.EnvVar, "pwsh")
	for name, script := range map[string]string{
		"hooks off":     short(add),
		"crlf":          strings.ReplaceAll(short(add), "\n", "\r\n"),
		"plain":         short(`git -C "$PROJECT_DIR" worktree add --detach "$ITERION_WORKSPACE" HEAD`),
		"two settings":  short(`git -C "$PROJECT_DIR" -c core.hooksPath=/dev/null -c core.fsmonitor=false worktree add --detach "$ITERION_WORKSPACE" HEAD`),
		"with comments": "# seed\n" + short(add),
	} {
		cfg := mk(script)
		if !MigrateLegacySeedHook(cfg) {
			t.Errorf("%s: the shortened sh preset must be migrated on a PowerShell host", name)
			continue
		}
		h := cfg.Hooks.AfterCreate
		if h.Shell != "pwsh" || !strings.Contains(h.Script, `$projectDir = 'C:\Users\Shket\SELF Repository\bars-project'`) {
			t.Errorf("%s: migrated hook = %+v", name, h)
		}
	}

	for name, script := range map[string]string{
		"extra command":     short(add) + "echo extra\n",
		"other argument":    short(`git -C "$PROJECT_DIR" worktree add --detach "$ITERION_WORKSPACE" main`),
		"force flag":        short(`git -C "$PROJECT_DIR" worktree add --force --detach "$ITERION_WORKSPACE" HEAD`),
		"substitution":      short(`git -C "$PROJECT_DIR" -c core.x=$(id) worktree add --detach "$ITERION_WORKSPACE" HEAD`),
		"another directory": short(`git -C /elsewhere worktree add --detach "$ITERION_WORKSPACE" HEAD`),
		"no set -e":         "PROJECT_DIR=" + fmt.Sprintf("%q", dir) + "\n" + add + "\n",
		"raw backslashes":   "set -e\nPROJECT_DIR=\"" + dir + "\"\n" + add + "\n",
	} {
		if MigrateLegacySeedHook(mk(script)) {
			t.Errorf("%s: an edited hook is the operator's: leave it", name)
		}
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
