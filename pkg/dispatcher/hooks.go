package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arketov/iterion/pkg/internal/hostshell"
	"github.com/arketov/iterion/pkg/internal/proc"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// Hook is a single lifecycle hook. Exactly one of Script or Path must be
// set: Script is an inline shell snippet, run by the host shell for this OS
// (`sh -lc` on Linux/macOS, PowerShell on Windows — see Shell); Path is a
// path to a script (the path itself is the command — on Unix the file's
// shebang controls the interpreter; on Windows it is run by PowerShell).
type Hook struct {
	Script    string `yaml:"script,omitempty" json:"script,omitempty"`
	Path      string `yaml:"path,omitempty" json:"path,omitempty"`
	TimeoutMS int    `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	// Shell picks the shell explicitly: sh, bash, pwsh, powershell or a path.
	// Empty means the host default (hostshell: sh on Linux/macOS, pwsh or
	// powershell.exe on Windows), overridable by ITERION_SHELL.
	Shell string `yaml:"shell,omitempty" json:"shell,omitempty"`
}

// Hooks groups the four workspace-lifecycle hooks. Any field can be nil.
type Hooks struct {
	AfterCreate  *Hook `yaml:"after_create,omitempty" json:"after_create,omitempty"`
	BeforeRun    *Hook `yaml:"before_run,omitempty" json:"before_run,omitempty"`
	AfterRun     *Hook `yaml:"after_run,omitempty" json:"after_run,omitempty"`
	BeforeRemove *Hook `yaml:"before_remove,omitempty" json:"before_remove,omitempty"`
}

// defaultHookTimeout caps any hook missing an explicit timeout_ms.
const defaultHookTimeout = 60 * time.Second

// hookDrainBound is this site's default bound on the wait for a hook's
// output once the hook itself has gone, overridable through
// [proc.WaitDelayEnv].
const hookDrainBound = 2 * time.Second

// Validate checks that each hook in the group sets exactly one of
// Script or Path.
func (h *Hooks) Validate() error {
	cases := []struct {
		name string
		hook *Hook
	}{
		{"after_create", h.AfterCreate},
		{"before_run", h.BeforeRun},
		{"after_run", h.AfterRun},
		{"before_remove", h.BeforeRemove},
	}
	for _, c := range cases {
		if c.hook == nil {
			continue
		}
		if (c.hook.Script == "") == (c.hook.Path == "") {
			return fmt.Errorf("hook %q: exactly one of script/path required", c.name)
		}
	}
	return nil
}

// Run executes the hook with cwd set to workspace and the additional
// env vars appended to the inherited environment. stdout/stderr lines
// are streamed to the logger. A nil hook is a no-op.
func (h *Hook) Run(ctx context.Context, logger *iterlog.Logger, name, workspace string, env []string) error {
	if h == nil {
		return nil
	}
	timeout := time.Duration(h.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := h.Script
	if command == "" {
		command = h.Path
	}
	if command == "" {
		return fmt.Errorf("hook %s: empty command", name)
	}

	cmd, err := hostshell.Command(cctx, hostshell.Request{Name: h.Shell}, command, true)
	if err != nil {
		return fmt.Errorf("hook %s: %w", name, err)
	}
	// Cancellation takes the whole group: a hook's backgrounded grandchild
	// would otherwise outlive the timeout, keep writing the workspace the
	// next lifecycle step is about to act on, and hold the inherited
	// stdout/stderr fds.
	proc.TerminateGroupOnCancel(cmd)
	// WaitDelay bounds the orphan-pipe wait for anything that escaped the
	// group (a descendant that made its own session). Without it, cmd.Run
	// blocks until that process exits naturally, defeating the timeout. 2s
	// is well below any sane TimeoutMS user setting, and
	// proc.WaitDelayEnv is the operator's override.
	drainBound := proc.SetWaitDelay(cmd, hookDrainBound)
	cmd.Dir = workspace
	// Inherit the host environment so hooks can find `git`, `gh`, …
	// on $PATH. The dispatcher-supplied env (ITERION_*) is appended so
	// it overrides any conflicting parent value.
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = hostshell.OutputFor(cmd, &stdout)
	cmd.Stderr = hostshell.OutputFor(cmd, &stderr)

	logger.Info("dispatcher: hook %s starting (cwd=%s timeout=%s)", name, workspace, timeout)
	start := time.Now()
	err = cmd.Run()
	dur := time.Since(start)

	if out := strings.TrimSpace(stdout.String()); out != "" {
		for _, line := range strings.Split(out, "\n") {
			logger.Info("dispatcher: hook %s: %s", name, line)
		}
	}
	if errOut := strings.TrimSpace(stderr.String()); errOut != "" {
		for _, line := range strings.Split(errOut, "\n") {
			logger.Warn("dispatcher: hook %s [stderr]: %s", name, line)
		}
	}

	if proc.DrainCutShort(err) {
		// The hook's verdict is its exit status, and the hook exited 0: what
		// kept the pipes open is a process it left running, and its output
		// is already logged above. Reported, never charged to the lifecycle
		// step — a succeeded hook failing the step was the defect.
		logger.Warn("dispatcher: hook %s exited, and a process it left kept its output open: stopped waiting %s",
			name, proc.WaitDelayHint(drainBound))
		err = nil
	}
	if err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return &HookError{Hook: name, ExitCode: -1, TimedOut: true, Timeout: timeout, Stderr: tailOutput(stderr.String()), Err: err}
		}
		he := &HookError{Hook: name, ExitCode: -1, Duration: dur, Stderr: tailOutput(stderr.String()), Err: err}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			he.ExitCode = exitErr.ExitCode()
		}
		return he
	}
	logger.Info("dispatcher: hook %s ok (%s)", name, dur)
	return nil
}

// HookError is the structured failure of a lifecycle hook. It keeps the
// historical message text ("hook <name>: <cause> (after <dur>)") so existing
// log greps keep working, and additionally carries WHICH hook failed and with
// WHAT exit code, so the dispatcher snapshot and the board can show them
// without parsing the string. ExitCode is -1 when the hook never produced an
// exit status (it could not start — e.g. the shell is missing — or it timed out).
type HookError struct {
	Hook     string
	ExitCode int
	TimedOut bool
	Timeout  time.Duration
	Duration time.Duration
	// Stderr is the tail of the hook's stderr, the line that usually names the cause.
	Stderr string
	Err    error
}

func (e *HookError) Error() string {
	if e.TimedOut {
		return fmt.Sprintf("hook %s: timeout after %s", e.Hook, e.Timeout)
	}
	return fmt.Sprintf("hook %s: %v (after %s)", e.Hook, e.Err, e.Duration)
}

func (e *HookError) Unwrap() error { return e.Err }

// hookErrorTailBytes bounds the stderr tail kept on a HookError.
const hookErrorTailBytes = 2048

func tailOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > hookErrorTailBytes {
		s = "…" + s[len(s)-hookErrorTailBytes:]
	}
	return s
}
