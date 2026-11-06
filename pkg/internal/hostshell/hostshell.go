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
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/arketov/iterion/internal/envtrust"
	"github.com/arketov/iterion/pkg/internal/proc"
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

// Source says why a shell was picked, so an error or a log line can name
// the choice that led to it.
type Source string

const (
	// SourceExplicit is a Request's own Name (a hook's `shell:`).
	SourceExplicit Source = "explicit"
	// SourceEnv is the ITERION_SHELL environment variable.
	SourceEnv Source = "env"
	// SourceDefault is the platform default (sh on POSIX, PowerShell on Windows).
	SourceDefault Source = "default"
	// SourcePosixScript is an sh-written script kept on sh on a PowerShell host
	// (LooksPosix).
	SourcePosixScript Source = "posix-script"
	// SourceShebang is the `#!` line of a script file.
	SourceShebang Source = "shebang"
	// SourceExtension is the extension of a script file.
	SourceExtension Source = "extension"
)

// Shell is a resolved shell: the dialect and the executable to start.
type Shell struct {
	Kind Kind
	// Path is what exec starts: an absolute path when it was looked up, a
	// bare name for a POSIX default that is left to exec.
	Path string
	// Source says why this shell was picked (see Source).
	Source Source
	// Note, when set, says why this shell is not the one the platform default
	// would have been (a sh-written script kept on sh): callers may log it.
	Note string
}

// Request says what the caller wants.
type Request struct {
	// Name is an explicit choice: "sh", "bash", "pwsh", "powershell", a
	// path, or ""/"auto" for the platform default.
	Name string
	// PosixDefault is the shell used on non-Windows when Name is empty:
	// KindSh (the default of the zero value) or KindBash.
	PosixDefault Kind
	// Script, when set and Name is empty, is the text about to run. A text
	// that is plainly sh (LooksPosix) on a host whose default is PowerShell is
	// a script written before the default changed — a saved dispatcher config,
	// a plugin command: it runs under sh/bash when one is on PATH (Shell.Note
	// says so) and otherwise fails with a *PosixScriptError naming the remedy,
	// instead of being fed to PowerShell, which misreads it. Leave it empty for
	// text written for the platform shell (a tool node's `command:`).
	Script string
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

// Name is the shell's executable name without directory and `.exe`, in lower
// case ("pwsh", "powershell", "sh", "bash"): what a message shows next to Path.
func (s Shell) Name() string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(s.Path, `\`, "/")))
	return strings.TrimSuffix(base, ".exe")
}

// RequestedShell is the explicit shell choice behind req — its own Name, else
// ITERION_SHELL — and where it came from. name is empty (and src is
// SourceDefault) when nothing was chosen. Resolve's first step, exported so
// an error can name a choice that could not be started.
func RequestedShell(req Request) (name string, src Source) {
	name = strings.TrimSpace(req.Name)
	if name != "" && !strings.EqualFold(name, "auto") {
		return name, SourceExplicit
	}
	name = strings.TrimSpace(getenv(EnvVar))
	if name != "" && !strings.EqualFold(name, "auto") {
		return name, SourceEnv
	}
	return "", SourceDefault
}

func resolve(req Request, platform string) (Shell, error) {
	name, src := RequestedShell(req)
	if name == "" {
		sh, err := defaultShell(req, platform)
		if err == nil && sh.Kind == KindPowerShell && req.Script != "" && LooksPosix(req.Script) {
			return posixForLegacyScript(platform)
		}
		if err != nil && platform == "windows" && req.Script != "" && LooksPosix(req.Script) {
			return posixForLegacyScript(platform)
		}
		return sh, err
	}
	sh, err := named(name, platform)
	if err == nil {
		sh.Source = src
	}
	return sh, err
}

// PosixScriptError says a script written for sh meets a host whose command
// shell is PowerShell and no POSIX shell is installed to run it.
type PosixScriptError struct{}

func (*PosixScriptError) Error() string {
	return "this script is written for sh, but commands on this host run in PowerShell and no sh or bash is on PATH: " +
		"rewrite it in PowerShell, set `shell: pwsh` on it if it already is, install Git for Windows (sh) and set `shell: sh`, " +
		"or choose the shell with " + EnvVar
}

func (*PosixScriptError) Is(target error) bool { return target == exec.ErrNotFound }

func posixForLegacyScript(platform string) (Shell, error) {
	for _, n := range []string{"sh", "bash"} {
		p, err := lookPath(n)
		if err != nil || strings.Contains(strings.ToLower(p), `\system32\`) {
			// System32\bash.exe is the WSL launcher, not a shell of this host.
			continue
		}
		kind := KindSh
		if n == "bash" {
			kind = KindBash
		}
		return Shell{Kind: kind, Path: p, Source: SourcePosixScript, Note: "the script is written for sh and has no `shell:`: running it under " + n +
			" as before; add `shell: " + n + "` to say so, or rewrite it for PowerShell"}, nil
	}
	return Shell{}, &PosixScriptError{}
}

// posixLine matches a line only a POSIX shell can read: `set -e`, a VAR=value
// assignment, `export`, a shebang, a `[ ... ]` test, or the closing words of
// an sh block. PowerShell variables start with `$`, so none of these is
// PowerShell. `set -Name x` (Set-Variable's alias) is told from `set -e` by
// what follows the flag: only a bare flag or `-o name` ends the line.
var posixLine = regexp.MustCompile(`^(set\s+(-[a-zA-Z]+|-o\s+\w+)\s*(;|&&|\|\||$)|[A-Za-z_][A-Za-z0-9_]*=\S|export\s+[A-Za-z_]|#!\s*/\S*(ba)?sh\b|(if|elif|while|until)\s+(!\s+)?\[|\[\[?\s.*\s\]\]?\s*(&&|\|\||;|$)|fi\s*$|done\s*$|esac\s*$)`)

// LooksPosix reports whether script contains a line only a POSIX shell can
// read (see posixLine). Conservative: false for anything ambiguous. Lines
// inside a PowerShell here-string (@" ... "@, @' ... '@), a block comment
// (<# ... #>, where comment-based help carries `.EXAMPLE NAME=value` lines) or
// a hashtable (@{ ... }, whose braces are counted — a `{ ... }` script block
// inside it does not close it) are data, comments or `Name=value` pairs of
// PowerShell's own and are not read. A multi-line "..." string is not tracked:
// put `shell: pwsh` on such a hook.
func LooksPosix(script string) bool {
	here := ""       // the closing token of the here-string being skipped
	inBlock := false // inside a <# ... #> comment
	hash := 0        // unclosed braces of the @{ ... } hashtable being skipped
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if here != "" {
			if strings.HasPrefix(line, here) {
				here = ""
			}
			continue
		}
		if inBlock {
			if strings.Contains(line, "#>") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(line, "<#") {
			if !strings.Contains(line[2:], "#>") {
				inBlock = true
			}
			continue
		}
		if strings.HasSuffix(line, `@"`) {
			here = `"@`
			continue
		}
		if strings.HasSuffix(line, `@'`) {
			here = `'@`
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#!") {
			continue
		}
		if hash > 0 {
			// Inside a hashtable every brace counts: a `{ ... }` script block
			// in it does not close it.
			hash += strings.Count(line, "{") - strings.Count(line, "}")
			if hash < 0 {
				hash = 0
			}
			continue
		}
		if strings.Contains(line, "@{") {
			// The opening line counts the `@{` only (an sh line that merely
			// holds an `@{u}` and a stray `{` is not a hashtable).
			hash += strings.Count(line, "@{") - strings.Count(line, "}")
			if hash < 0 {
				hash = 0
			}
			continue
		}
		if posixLine.MatchString(line) {
			return true
		}
	}
	return false
}

func defaultShell(req Request, platform string) (Shell, error) {
	if platform != "windows" {
		if req.PosixDefault == KindBash {
			return Shell{Kind: KindBash, Path: "bash", Source: SourceDefault}, nil
		}
		return Shell{Kind: KindSh, Path: "sh", Source: SourceDefault}, nil
	}
	for _, n := range []string{"pwsh", "powershell"} {
		if p, err := lookPath(n); err == nil {
			return Shell{Kind: KindPowerShell, Path: p, Source: SourceDefault}, nil
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
// is capped at 32767 and Go escapes each quote and the backslashes before it
// (worst case it doubles the script), so a longer script is streamed on stdin
// and read by psStdinBootstrap. -EncodedCommand is deliberately not used:
// powershell.exe 5.1 answers it with its error and warning streams serialised
// as CLIXML on stderr, which no flag turns off, whereas a plain -Command keeps
// stderr text.
const psInlineLimit = 12000

// psPrelude makes the child's output UTF-8 whatever the console code page
// (5.1 defaults to the OEM page), plain text (pwsh 7.2+ colours tables and
// errors with ANSI even into a pipe) and free of progress records. Wrapped so a
// host without a console cannot fail the command.
const psPrelude = "try{[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)}catch{};$ProgressPreference='SilentlyContinue';if($PSStyle){$PSStyle.OutputRendering='PlainText'}\n"

// psStdinBootstrap runs a script too long for the command line. It reads the
// whole of stdin (the script, UTF-8 whatever the console code page) to EOF
// before the script's first statement runs, so a native program the script
// starts finds stdin at EOF instead of eating the rest of the script, and
// dot-sources it in the session scope, as an inline -Command body runs.
const psStdinBootstrap = ". ([scriptblock]::Create([IO.StreamReader]::new([Console]::OpenStandardInput(),[Text.UTF8Encoding]::new($false)).ReadToEnd()))"

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
			return append(argv, "-Command", psPrelude+psStdinBootstrap), strings.NewReader(script + psEpilogue)
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
	cmd := proc.CommandContext(ctx, argv[0], argv[1:]...)
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

// OutputsFor is OutputFor for a command's stdout and stderr together. When
// the caller hands the same writer for both, they stay ONE writer (one
// wrapper for both fields): os/exec serialises writes only for identical
// Stdout and Stderr, and the caller's writer may rely on that.
func OutputsFor(cmd *exec.Cmd, stdout, stderr io.Writer) (io.Writer, io.Writer) {
	if sameWriter(stdout, stderr) {
		w := OutputFor(cmd, stdout)
		return w, w
	}
	return OutputFor(cmd, stdout), OutputFor(cmd, stderr)
}

func sameWriter(a, b io.Writer) bool {
	if a == nil || b == nil {
		return false
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	return ta == tb && ta.Comparable() && a == b
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
