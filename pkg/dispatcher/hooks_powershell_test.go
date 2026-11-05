package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/internal/hostshelltest"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// The PowerShell twins of the sh hook tests: the same mechanics (env, exit
// status, stderr tail, output lines) under the Windows default shell, and the
// explicit `shell:` field.

func TestHookRun_PowerShell_EnvAndOutput(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	dir := t.TempDir()
	var logs bytes.Buffer
	h := &Hook{Script: "Set-Content -NoNewline -Path out.txt -Value $env:ITERION_TEST_VAR; Write-Output 'hook says hi'"}
	err := h.Run(context.Background(), iterlog.New(iterlog.LevelInfo, &logs), "env", dir, []string{"ITERION_TEST_VAR=greenlight"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "greenlight" {
		t.Fatalf("env not propagated: %q", data)
	}
	if !strings.Contains(logs.String(), "hook says hi") || strings.Contains(logs.String(), "\r") {
		t.Fatalf("hook output must be logged without a stray CR: %q", logs.String())
	}
}

func TestHookRun_PowerShell_FailureIsStructured(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	h := &Hook{Script: "[Console]::Error.WriteLine('boom'); exit 7"}
	err := h.Run(context.Background(), newTestLogger(), "after_create", t.TempDir(), nil)
	var he *HookError
	if !errors.As(err, &he) {
		t.Fatalf("want *HookError, got %v", err)
	}
	if he.ExitCode != 7 || he.Stderr != "boom" {
		t.Fatalf("HookError = %+v, want exit=7 stderr=boom", he)
	}
}

func TestHookRun_ExplicitShellFieldWins(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	// The environment says PowerShell; the hook's own `shell:` is what runs.
	t.Setenv("ITERION_SHELL", "pwsh")
	h := &Hook{Script: "exit 3", Shell: "no-such-shell-xyz"}
	err := h.Run(context.Background(), newTestLogger(), "x", t.TempDir(), nil)
	var he *HookError
	if err == nil || errors.As(err, &he) && he.ExitCode == 3 {
		t.Fatalf("shell: must beat ITERION_SHELL; got %v", err)
	}
}
