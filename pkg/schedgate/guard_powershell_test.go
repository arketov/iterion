package schedgate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/internal/hostshelltest"
)

// The PowerShell twins of the sh guard tests: what the Windows default shell
// must give a guard — stdout the way sh gives it (LF), the exit code of an
// `exit N`, the working directory, plain-text stderr.

func TestRunGuard_PowerShell_StdoutIsLFAndExitCodeKept(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	res := RunGuard(context.Background(), GuardSpec{Command: "Write-Output 'v42'", Dir: t.TempDir()})
	if res.Kind != GuardOK || res.Stdout != "v42\n" {
		t.Fatalf("Kind=%v Stdout=%q err=%v, want GuardOK and %q", res.Kind, res.Stdout, res.Err, "v42\n")
	}
	res = RunGuard(context.Background(), GuardSpec{Command: "[Console]::Error.WriteLine('nope'); exit 7", Dir: t.TempDir()})
	if res.Kind != GuardBlocked || res.ExitCode != 7 {
		t.Fatalf("Kind=%v ExitCode=%d, want GuardBlocked and 7", res.Kind, res.ExitCode)
	}
	if !strings.Contains(res.StderrTail, "nope") || strings.Contains(res.StderrTail, "CLIXML") || strings.Contains(res.StderrTail, "\r") {
		t.Fatalf("StderrTail = %q, want plain text carrying %q", res.StderrTail, "nope")
	}
}

func TestRunGuard_PowerShell_WorkingDirectoryAndEnv(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	dir := t.TempDir()
	res := RunGuard(context.Background(), GuardSpec{
		Command: "(Get-Location).Path; $env:ITERION_GUARD_PROBE",
		Dir:     dir,
		Env:     []string{"ITERION_GUARD_PROBE=probe-ok"},
	})
	if res.Kind != GuardOK {
		t.Fatalf("Kind=%v err=%v", res.Kind, res.Err)
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 2 || lines[1] != "probe-ok" {
		t.Fatalf("Stdout = %q, want the directory then probe-ok", res.Stdout)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(lines[0])
	if !strings.EqualFold(got, want) {
		t.Fatalf("pwd = %q, want %q", lines[0], dir)
	}
}

func TestRunGuard_PowerShell_NativeFailureKeepsItsExitCode(t *testing.T) {
	hostshelltest.UsePowerShell(t)
	// A program that fails as the script's LAST statement: pwsh -Command alone
	// would report 1; the guard's verdict is the program's own code.
	res := RunGuard(context.Background(), GuardSpec{Command: "git --no-such-flag-xyz", Dir: t.TempDir()})
	if res.Kind != GuardBlocked || res.ExitCode == 0 || res.ExitCode == 1 {
		t.Fatalf("Kind=%v ExitCode=%d, want blocked with git's own code (129)", res.Kind, res.ExitCode)
	}
}
