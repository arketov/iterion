package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/internal/hostshell"
)

// A shell that cannot be found is a *HookError — the hook is named, so the
// retry queue and launch_failures can show it — saying what was asked for.
func TestHookRun_MissingShellIsAHookError(t *testing.T) {
	h := &Hook{Script: "echo hi", Shell: missingShell(t)}
	err := h.Run(context.Background(), newTestLogger(), "after_create", t.TempDir(), nil)
	var he *HookError
	if !errors.As(err, &he) {
		t.Fatalf("want *HookError, got %T: %v", err, err)
	}
	if he.Hook != "after_create" || !he.NotStarted || he.ExitCode != -1 || he.TimedOut {
		t.Fatalf("HookError = %+v", he)
	}
	if he.ShellSource != string(hostshell.SourceExplicit) || he.Shell != h.Shell {
		t.Fatalf("shell = %q source = %q, want the requested %q / explicit", he.Shell, he.ShellSource, h.Shell)
	}
	msg := err.Error()
	if !strings.Contains(msg, "hook after_create:") || strings.Contains(msg, "(after ") {
		t.Fatalf("message %q: want the hook named and no bogus duration", msg)
	}
	if !strings.Contains(msg, "[shell: ") {
		t.Fatalf("message %q does not say which shell was asked for", msg)
	}
}

func TestHookErrorMessageNamesTheShell(t *testing.T) {
	e := &HookError{Hook: "before_run", ExitCode: 2, Err: errors.New("exit status 2"),
		Shell: "pwsh", ShellPath: `C:\Program Files\PowerShell\7\pwsh.exe`, ShellSource: "default"}
	msg := e.Error()
	for _, want := range []string{"hook before_run: exit status 2", "pwsh", `C:\Program Files\PowerShell\7\pwsh.exe`, "platform default"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	bare := (&HookError{Hook: "x", ExitCode: 1, Err: errors.New("boom")}).Error()
	if strings.Contains(bare, "[shell") {
		t.Errorf("a hook with no known shell must not print a shell tag: %q", bare)
	}
	if got := ShellLabel("", "", "default"); got != "" {
		t.Errorf("ShellLabel of nothing = %q, want empty", got)
	}
	if got := ShellLabel("sh", "sh", "env"); got != "sh, ITERION_SHELL" {
		t.Errorf("ShellLabel = %q", got)
	}
}

// The shell of the failed hook rides into the retry entry and the snapshot
// row the studio reads.
func TestScheduleRetryCarriesTheHookShell(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	cause := &HookError{Hook: "after_create", ExitCode: 128, Err: errors.New("exit status 128"),
		Shell: "pwsh", ShellPath: `C:\p\pwsh.exe`, ShellSource: "default"}
	c.scheduleRetry("i1", &runningEntry{IssueID: "i1", Identifier: "T-1"}, fmt.Errorf("after_create hook: %w", cause))
	e := c.state.retries["i1"]
	if e == nil {
		t.Fatal("no retry queued")
	}
	t.Cleanup(func() {
		if e.Timer != nil {
			e.Timer.Stop()
		}
	})
	if e.LastHook != "after_create" || e.LastHookShell != "pwsh" || e.LastHookShellPath != `C:\p\pwsh.exe` || e.LastHookShellSource != "default" {
		t.Fatalf("retry entry = %+v", e)
	}
	snap := c.buildSnapshot()
	if len(snap.Retries) != 1 {
		t.Fatalf("retries = %d", len(snap.Retries))
	}
	r := snap.Retries[0]
	if r.HookShell != "pwsh" || r.HookShellPath != `C:\p\pwsh.exe` || r.HookShellSource != "default" {
		t.Fatalf("RetryView = %+v", r)
	}
}

// path: names a script FILE. One in a directory with a space in its name is
// not split into a command and an argument.
func TestHookRun_PathWithASpaceRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a .sh needs sh on Windows; the Windows file rules are covered in hostshell")
	}
	dir := filepath.Join(t.TempDir(), "my scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran.txt")
	script := filepath.Join(dir, "prepare.sh")
	body := "#!/bin/sh\necho ok > '" + marker + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Hook{Path: script}
	if err := h.Run(context.Background(), newTestLogger(), "after_create", t.TempDir(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the script in a directory with a space did not run: %v", err)
	}
}

// A Path that is not an existing file stays command text, as before.
func TestHookScriptFileOnlyForExistingRegularFiles(t *testing.T) {
	ws := t.TempDir()
	if got := (&Hook{Path: "./missing.sh"}).scriptFile(ws); got != "" {
		t.Errorf("a missing path is command text, got file %q", got)
	}
	if got := (&Hook{Path: "echo hi"}).scriptFile(ws); got != "" {
		t.Errorf("a command with arguments is command text, got file %q", got)
	}
	if got := (&Hook{Script: "echo", Path: "x"}).scriptFile(ws); got != "" {
		t.Errorf("an inline script is never a file, got %q", got)
	}
	f := filepath.Join(ws, "prepare.ps1")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (&Hook{Path: "prepare.ps1"}).scriptFile(ws); got != f {
		t.Errorf("a relative existing file must resolve against the workspace: got %q, want %q", got, f)
	}
}
