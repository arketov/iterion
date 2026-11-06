package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/arketov/iterion/pkg/internal/hostshell"
	"github.com/arketov/iterion/pkg/internal/proc"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// Hook is a single lifecycle hook. Exactly one of Script or Path must be
// set: Script is an inline shell snippet, run by the host shell for this OS
// (`sh -lc` on Linux/macOS, PowerShell on Windows — see Shell); Path is a
// path to a script FILE, run by the interpreter `shell:` names, else (Windows)
// its `#!` line, else its extension (.ps1 PowerShell, .sh sh/bash, .py python,
// .js node, .exe/.bat/.cmd itself — an unknown one is an error, see
// hostshell.FileCommand); on Unix with none of those the file's shebang and
// executable bit decide, as before. A Path that is not an existing file is
// run as command text.
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

// byName returns the hook called name (after_create, before_run, after_run,
// before_remove), nil for an unknown or unset one.
func (h *Hooks) byName(name string) *Hook {
	switch name {
	case "after_create":
		return h.AfterCreate
	case "before_run":
		return h.BeforeRun
	case "after_run":
		return h.AfterRun
	case "before_remove":
		return h.BeforeRemove
	}
	return nil
}

// hookNames is the four lifecycle hooks, in the order they run.
var hookNames = []string{"after_create", "before_run", "after_run", "before_remove"}

// PinHeuristicShells writes `shell:` onto every inline hook that has none and
// that hostshell would run under sh only because its text looks like sh on a
// PowerShell host (hostshell.SourcePosixScript): the shell it already runs
// under, named, so the choice is explicit instead of re-guessed from the text
// on every run. It changes no behaviour — a hook that runs under sh keeps
// running under sh — and never touches a hook with a `shell:`, one that reads
// as PowerShell (the platform default needs no pin), one chosen by
// ITERION_SHELL, or one for which no sh is installed (that one reports it when
// it runs). Returns the names of the hooks it pinned.
func PinHeuristicShells(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var pinned []string
	for _, name := range hookNames {
		h := cfg.Hooks.byName(name)
		if h == nil || h.Script == "" || h.Shell != "" {
			continue
		}
		sh, err := hostshell.Resolve(hostshell.Request{Script: h.Script})
		if err != nil || sh.Source != hostshell.SourcePosixScript {
			continue
		}
		h.Shell = sh.Name()
		pinned = append(pinned, name)
	}
	return pinned
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

	var (
		sh  hostshell.Shell
		cmd *exec.Cmd
	)
	if file := h.scriptFile(workspace); file != "" {
		// `path:` names a script FILE: run it with the interpreter its
		// `shell:`, `#!` line or extension names, never as command text (on
		// Windows PowerShell would hand a .sh to the file association and
		// report success without running it).
		argv, fsh, ferr := hostshell.FileCommand(file, h.Shell)
		if ferr != nil {
			return h.notStarted(name, ferr, false)
		}
		sh = fsh
		cmd = exec.CommandContext(cctx, argv[0], argv[1:]...)
	} else {
		var err error
		sh, err = hostshell.Resolve(hostshell.Request{Name: h.Shell, Script: command})
		if err != nil {
			return h.notStarted(name, err, true)
		}
		if sh.Note != "" {
			logger.Warn("dispatcher: hook %s: %s", name, sh.Note)
		}
		cmd = sh.Command(cctx, command, true)
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

	logger.Info("dispatcher: hook %s starting (cwd=%s timeout=%s shell=%s)", name, workspace, timeout, describeShell(sh))
	start := time.Now()
	err := cmd.Run()
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
			he := &HookError{Hook: name, ExitCode: -1, TimedOut: true, Timeout: timeout, Stderr: tailOutput(stderr.String()), Err: err}
			he.setShell(sh)
			return he
		}
		he := &HookError{Hook: name, ExitCode: -1, Duration: dur, Stderr: tailOutput(stderr.String()), Err: err}
		he.setShell(sh)
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
// log greps keep working, and additionally carries WHICH hook failed, with
// WHAT exit code and under WHICH shell, so the dispatcher snapshot and the
// board can show them without parsing the string. ExitCode is -1 when the hook
// never produced an exit status (it could not start — e.g. the shell is
// missing — or it timed out).
type HookError struct {
	Hook     string
	ExitCode int
	TimedOut bool
	// NotStarted is true when nothing was started: no shell was found for the
	// hook, or its script file has no interpreter. Err says why.
	NotStarted bool
	Timeout    time.Duration
	Duration   time.Duration
	// Stderr is the tail of the hook's stderr, the line that usually names the cause.
	Stderr string
	// Shell is the name of the shell or interpreter the hook ran under (pwsh,
	// powershell, sh, bash, python …), ShellPath the executable that was
	// started, and ShellSource why that one was picked (hostshell.Source:
	// explicit, env, default, posix-script, shebang, extension). For a hook
	// that could not start, Shell is the explicitly requested name, when there
	// was one, and ShellPath is empty.
	Shell       string
	ShellPath   string
	ShellSource string
	Err         error
}

func (e *HookError) Error() string {
	var msg string
	switch {
	case e.TimedOut:
		msg = fmt.Sprintf("hook %s: timeout after %s", e.Hook, e.Timeout)
	case e.NotStarted:
		msg = fmt.Sprintf("hook %s: %v", e.Hook, e.Err)
	default:
		msg = fmt.Sprintf("hook %s: %v (after %s)", e.Hook, e.Err, e.Duration)
	}
	if tag := ShellLabel(e.Shell, e.ShellPath, e.ShellSource); tag != "" {
		msg += " [shell: " + tag + "]"
	}
	return msg
}

func (e *HookError) Unwrap() error { return e.Err }

func (e *HookError) setShell(sh hostshell.Shell) {
	e.Shell, e.ShellPath, e.ShellSource = sh.Name(), sh.Path, string(sh.Source)
}

// ShellLabel renders a hook's shell for a message: `pwsh C:\Program
// Files\PowerShell\pwsh.exe, platform default`. Empty when nothing is known.
func ShellLabel(name, path, source string) string {
	if name == "" && path == "" {
		return ""
	}
	label := name
	if path != "" && path != name {
		if label != "" {
			label += " "
		}
		label += path
	}
	switch hostshell.Source(source) {
	case hostshell.SourceExplicit:
		label += ", `shell:` on the hook"
	case hostshell.SourceEnv:
		label += ", " + hostshell.EnvVar
	case hostshell.SourceDefault:
		label += ", platform default"
	case hostshell.SourcePosixScript:
		label += ", sh-written script kept on sh"
	case hostshell.SourceShebang:
		label += ", the file's #! line"
	case hostshell.SourceExtension:
		label += ", the file's extension"
	}
	return label
}

// describeShell is the short form of a resolved shell for a log line.
func describeShell(sh hostshell.Shell) string {
	return ShellLabel(sh.Name(), sh.Path, string(sh.Source))
}

// notStarted is the HookError of a hook for which no process could be started
// (no shell, no interpreter for its script file). The explicit shell choice
// that failed — the hook's `shell:`, and for hook text ITERION_SHELL too (a
// script file's interpreter is not chosen by it) — is named, so the error says
// what was asked for.
func (h *Hook) notStarted(name string, cause error, textHook bool) *HookError {
	he := &HookError{Hook: name, ExitCode: -1, NotStarted: true, Err: cause}
	var req string
	var src hostshell.Source
	if textHook {
		req, src = hostshell.RequestedShell(hostshell.Request{Name: h.Shell})
	} else if n := strings.TrimSpace(h.Shell); n != "" && !strings.EqualFold(n, "auto") {
		req, src = n, hostshell.SourceExplicit
	}
	if req != "" {
		he.Shell, he.ShellSource = req, string(src)
	}
	return he
}

// scriptFile reports the absolute path of the script file a path: hook names:
// non-empty only for a hook with no inline script whose Path is an existing
// regular file (relative to the workspace, the hook's working directory). A
// Path that is not a file — `./run.sh --flag`, a command on PATH — stays
// command text, as it always was.
func (h *Hook) scriptFile(workspace string) string {
	if h.Script != "" || h.Path == "" {
		return ""
	}
	p := h.Path
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return p
}

// hookErrorTailBytes bounds the stderr tail kept on a HookError.
const hookErrorTailBytes = 2048

func tailOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > hookErrorTailBytes {
		s = "…" + s[len(s)-hookErrorTailBytes:]
	}
	return s
}
