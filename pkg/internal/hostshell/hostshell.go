// Package hostshell is the one place that decides which shell runs a command
// string on the HOST machine (a dispatcher hook, a plugin lifecycle command,
// a schedule guard, a tool node's `command:`, a settings hook).
//
// The rule, from the project owner (2026-11-05):
//
//   - Linux and macOS: unchanged — `sh` (login flag where the caller had
//     one) or `bash` for the callers that were pinned to bash.
//   - Windows: PowerShell — `pwsh` (PowerShell 7+) when it is on PATH, else
//     the built-in `powershell.exe` (5.1). Never `sh`/`bash`: Git for Windows
//     is not a dependency of a native Windows program, and its `usr\bin` is
//     not on the PATH of a desktop app.
//
// An explicit choice always wins: a Request's Name (a hook's `shell:` field)
// or, when that is empty, the ITERION_SHELL environment variable. A name that
// cannot be found is a typed *NotFoundError saying what was looked for and
// what to do about it — never a bare "executable file not found".
//
// Commands that run INSIDE a sandbox container are not this package's
// business: the container is Linux and keeps its own `sh`/`bash`.
package hostshell

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/arketov/iterion/internal/envtrust"
)

// EnvVar is the operator's global override of the shell choice: a name
// (sh, bash, pwsh, powershell) or a path to a shell binary. A Request's own
// Name wins over it.
const EnvVar = "ITERION_SHELL"

// Kind is the dialect a shell speaks, which fixes how a command string is
// handed to it.
type Kind string

const (
	// KindSh is a POSIX `sh`.
	KindSh Kind = "sh"
	// KindBash is bash.
	KindBash Kind = "bash"
	// KindPowerShell is PowerShell, 7+ (`pwsh`) or 5.1 (`powershell.exe`).
	KindPowerShell Kind = "powershell"
)

// Shell is a resolved shell: the dialect and the executable to start.
type Shell struct {
	Kind Kind
	// Path is what exec starts: an absolute path when it was looked up, a
	// bare name for a POSIX default that is left to exec.
	Path string
}

// Request says what the caller wants.
type Request struct {
	// Name is an explicit choice: "sh", "bash", "pwsh", "powershell", a
	// path, or ""/"auto" for the platform default.
	Name string
	// PosixDefault is the shell used on non-Windows when Name is empty:
	// KindSh (the default of the zero value) or KindBash.
	PosixDefault Kind
}

// NotFoundError says no shell could be started, naming what was looked for.
type NotFoundError struct {
	// Looked is every name tried, in order.
	Looked []string
	// Requested is the explicit choice that failed, "" for the default.
	Requested string
	goos      string
}

func (e *NotFoundError) Error() string {
	var b strings.Builder
	if e.Requested != "" {
		fmt.Fprintf(&b, "shell %q not found on PATH", e.Requested)
	} else {
		fmt.Fprintf(&b, "no command shell found (looked for %s on PATH)", strings.Join(e.Looked, ", "))
	}
	switch {
	case e.Requested != "":
		fmt.Fprintf(&b, ": install it, put it on PATH, or choose another shell (`shell:` on the hook, or %s)", EnvVar)
	case e.goos == "windows":
		fmt.Fprintf(&b, ": install PowerShell 7 (winget install Microsoft.PowerShell) or make sure powershell.exe is on PATH; set %s to use another shell", EnvVar)
	default:
		fmt.Fprintf(&b, ": install a POSIX shell or set %s to the shell to use", EnvVar)
	}
	return b.String()
}

// Is lets errors.Is(err, exec.ErrNotFound) hold for callers that test it.
func (e *NotFoundError) Is(target error) bool { return target == exec.ErrNotFound }

// seams, replaced by tests.
var (
	goos     = runtime.GOOS
	lookPath = envtrust.LookPath
	getenv   = os.Getenv
)

// Resolve picks the shell for req.
func Resolve(req Request) (Shell, error) {
	return resolve(req, goos)
}

func resolve(req Request, platform string) (Shell, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || strings.EqualFold(name, "auto") {
		name = strings.TrimSpace(getenv(EnvVar))
		if strings.EqualFold(name, "auto") {
			name = ""
		}
	}
	if name == "" {
		return defaultShell(req, platform)
	}
	return named(name, platform)
}

func defaultShell(req Request, platform string) (Shell, error) {
	if platform != "windows" {
		if req.PosixDefault == KindBash {
			return Shell{Kind: KindBash, Path: "bash"}, nil
		}
		return Shell{Kind: KindSh, Path: "sh"}, nil
	}
	for _, n := range []string{"pwsh", "powershell"} {
		if p, err := lookPath(n); err == nil {
			return Shell{Kind: KindPowerShell, Path: p}, nil
		}
	}
	return Shell{}, &NotFoundError{Looked: []string{"pwsh", "powershell"}, goos: platform}
}

func named(name, platform string) (Shell, error) {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	base = strings.TrimSuffix(base, ".exe")
	var kind Kind
	switch base {
	case "pwsh", "powershell":
		kind = KindPowerShell
	case "bash":
		kind = KindBash
	default:
		// sh, dash, zsh, ash, busybox sh … all take `-c script`.
		kind = KindSh
	}
	// A bare POSIX name on a POSIX host is left to exec, as it always was.
	if platform != "windows" && !strings.ContainsAny(name, `/\`) && kind != KindPowerShell {
		return Shell{Kind: kind, Path: name}, nil
	}
	p, err := lookPath(name)
	if err != nil {
		return Shell{}, &NotFoundError{Looked: []string{name}, Requested: name, goos: platform}
	}
	return Shell{Kind: kind, Path: p}, nil
}

// psInlineLimit is the longest script (characters, prelude and epilogue
// included) handed to PowerShell on the command line; a Windows command line
// is capped at 32767, so a longer script is streamed on stdin instead.
// -EncodedCommand is deliberately not used: powershell.exe 5.1 answers it
// with its error and warning streams serialised as CLIXML on stderr, which
// no flag turns off, whereas a plain -Command keeps stderr text.
const psInlineLimit = 24000

// psPrelude makes the child's output UTF-8 whatever the console code page
// (5.1 defaults to the OEM page), plain text (pwsh 7.2+ colours tables and
// errors with ANSI even into a pipe) and free of progress records. Wrapped so a
// host without a console cannot fail the command.
const psPrelude = "try{[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)}catch{};$ProgressPreference='SilentlyContinue';if($PSStyle){$PSStyle.OutputRendering='PlainText'}\n"

// psEpilogue keeps a native command's exit code. `pwsh -Command` collapses
// every failure to 1; a hook that exits 2 through `exit 2` is unaffected,
// and a script whose last statement is a failing program exits with that
// program's code, as `sh -c` does.
const psEpilogue = "\nif (-not $?) { if ($LASTEXITCODE) { exit $LASTEXITCODE } else { exit 1 } }"

var psFlags = []string{"-NoLogo", "-NoProfile", "-NonInteractive"}

// Argv returns the argv (starting with the shell) that runs script, and a
// stdin to attach when the script is streamed instead of passed inline (nil
// otherwise: the caller's own stdin choice stands). login asks a POSIX shell
// for `-l`; PowerShell never loads a profile.
func (s Shell) Argv(script string, login bool) (argv []string, stdin io.Reader) {
	switch s.Kind {
	case KindPowerShell:
		argv = append([]string{s.Path}, psFlags...)
		body := psPrelude + script + psEpilogue
		if len([]rune(body)) > psInlineLimit {
			return append(argv, "-Command", "-"), strings.NewReader(body)
		}
		return append(argv, "-Command", body), nil
	default:
		flag := "-c"
		if login {
			flag = "-lc"
		}
		return []string{s.Path, flag, script}, nil
	}
}

// Command builds the *exec.Cmd that runs script in this shell. A streamed
// script is already attached as cmd.Stdin.
func (s Shell) Command(ctx context.Context, script string, login bool) *exec.Cmd {
	argv, stdin := s.Argv(script, login)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	return cmd
}

// Command resolves req and builds the command in one step.
func Command(ctx context.Context, req Request, script string, login bool) (*exec.Cmd, error) {
	sh, err := Resolve(req)
	if err != nil {
		return nil, err
	}
	return sh.Command(ctx, script, login), nil
}

// SingleQuoteEscape is what goes where a `'` sits inside a single-quoted
// word: `'\''` for sh/bash (close, escaped quote, reopen), `''` on a Windows
// host, where the word is PowerShell's. The PowerShell form is also
// injection-safe if a POSIX shell reads it (the quote just closes and reopens
// the word, every other byte stays quoted) — it only costs the apostrophe,
// which matters for a Windows host running a command in a Linux sandbox.
func SingleQuoteEscape() string {
	if goos == "windows" {
		return "''"
	}
	return `'\''`
}

// IsWindowsHost reports whether commands on this host run through PowerShell
// by default (the portability rule's switch for callers that adapt their own
// scripts, e.g. the dispatcher's seed preset).
func IsWindowsHost() bool { return goos == "windows" }

// ScriptInterpreter maps a tool node's `language:` to the interpreter argv
// (the script file path is appended) and the file extension, for a script
// run on the host. ok is false for an unknown language. Languages js/node
// and py/python/python3 are the same everywhere. The shell languages follow
// the platform rule: empty means the platform shell (Resolve: sh on POSIX,
// PowerShell on Windows, or what ITERION_SHELL names); `powershell`/`pwsh`/`ps1` is PowerShell anywhere it exists;
// an explicit `sh`/`bash` is that shell wherever it can be found — on
// Windows it is looked up like any interpreter and a miss is a
// *NotFoundError, not a silent fallback.
func ScriptInterpreter(language string) (argv []string, ext string, ok bool, err error) {
	return scriptInterpreter(language, goos)
}

func scriptInterpreter(language, platform string) (argv []string, ext string, ok bool, err error) {
	switch language {
	case "js", "node":
		return []string{"node"}, ".js", true, nil
	case "py", "python", "python3":
		return []string{"python3", "-I"}, ".py", true, nil
	case "":
		// The platform shell — what Resolve says: ITERION_SHELL when set, else
		// sh on POSIX and PowerShell on Windows.
		sh, rerr := resolve(Request{}, platform)
		if rerr != nil {
			return nil, "", true, rerr
		}
		if sh.Kind == KindPowerShell {
			return psFile(platform)
		}
		return []string{sh.Path}, ".sh", true, nil
	case "powershell", "pwsh", "ps1":
		return psFile(platform)
	case "sh", "bash":
		if platform == "windows" {
			sh, rerr := named(language, platform)
			if rerr != nil {
				return nil, "", true, fmt.Errorf("%w (script language %q needs that shell on Windows; use `language: powershell`, or install it)", rerr, language)
			}
			return []string{sh.Path}, ".sh", true, nil
		}
		return []string{language}, ".sh", true, nil
	}
	return nil, "", false, nil
}

func psFile(platform string) ([]string, string, bool, error) {
	var sh Shell
	var err error
	if platform == "windows" {
		sh, err = defaultShell(Request{}, platform)
	} else {
		sh, err = named("pwsh", platform)
	}
	if err != nil {
		return nil, "", true, err
	}
	argv := append([]string{sh.Path}, psFlags...)
	return append(argv, "-ExecutionPolicy", "Bypass", "-File"), ".ps1", true, nil
}

// IsPowerShell reports whether cmd was built by this package for a
// PowerShell shell (its argv starts with pwsh/powershell and carries the
// flags Argv adds).
func IsPowerShell(cmd *exec.Cmd) bool {
	if cmd == nil || len(cmd.Args) < 2 {
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(cmd.Args[0], `\`, "/")), ".exe"))
	return (base == "pwsh" || base == "powershell") && cmd.Args[1] == psFlags[0]
}

// OutputFor is the writer to give cmd for one of its streams so a
// PowerShell-run command's output reads as it would from sh: its CRLF line
// ends become LF, which is what a variable, a guard's stdout or a log line
// built from the stream is expected to carry. Any other command's stream is
// w itself.
func OutputFor(cmd *exec.Cmd, w io.Writer) io.Writer {
	if !IsPowerShell(cmd) {
		return w
	}
	return &lfWriter{w: w}
}

// lfWriter rewrites CRLF to LF. A CR ending one Write is held until the next
// byte says whether it starts a CRLF; one that never gets an answer (the
// stream's last byte) is dropped, as PowerShell never writes it alone.
type lfWriter struct {
	w      io.Writer
	heldCR bool
}

func (l *lfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+1)
	for _, b := range p {
		if l.heldCR {
			l.heldCR = false
			if b != '\n' {
				out = append(out, '\r')
			}
		}
		if b == '\r' {
			l.heldCR = true
			continue
		}
		out = append(out, b)
	}
	if len(out) > 0 {
		if _, err := l.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
