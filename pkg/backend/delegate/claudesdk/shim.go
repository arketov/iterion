package claudesdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// AdaptHostCmd makes a host-spawned claude command safe on Windows. It is for
// a [CommandBuilder] that builds the command itself (so the SDK's own
// findCLI/spawn checks never see it): exec.CommandContext has already resolved
// a bare "claude" through PATHEXT to the npm `claude.cmd` shim, and this
// swaps cmd.Path for the native claude.exe the shim wraps. When the shim
// cannot be resolved (a node-based shim) and cmd.exe would mangle the command
// line — a system prompt with a line break, or a command line past cmd.exe's
// length limit — the --system-prompt / --append-system-prompt value moves to a
// file made by spill and the flag becomes its --…-file form. A line break left
// in any other argument sets cmd.Err so Start fails loudly instead of running a
// command cmd.exe has truncated. A nil spill keeps only that refusal. A no-op
// off Windows and for any non-batch path; never call it for a sandbox
// (docker exec) command, whose Path is the runtime, not the CLI.
func AdaptHostCmd(cmd *exec.Cmd, spill SpillFunc) { adaptHostCmd(runtime.GOOS, cmd, spill) }

// SpillFunc writes content to a private file named after name and returns its
// path. The caller owns the file's lifetime: it must outlive the CLI's start.
type SpillFunc func(name string, content []byte) (path string, err error)

// batchShimCmdLineBudget is the command-line length above which a batch-file
// spawn spills the system prompt to a file. cmd.exe accepts 8191 characters
// (Microsoft Learn, "Command prompt line string limitation"); the margin
// leaves room for the shim path and `cmd /c`.
const batchShimCmdLineBudget = 8000

func adaptHostCmd(goos string, cmd *exec.Cmd, spill SpillFunc) {
	if cmd == nil || cmd.Err != nil || !isBatchShim(goos, cmd.Path) {
		return
	}
	if native := preferNativeBinary(goos, cmd.Path); native != cmd.Path {
		cmd.Path = native
		return
	}
	if len(cmd.Args) > 1 {
		if spill != nil && needsSpill(cmd.Args[1:]) {
			args, err := spillSystemPromptArgs(cmd.Args[1:], spill)
			if err != nil {
				cmd.Err = err
				return
			}
			cmd.Args = slices.Concat(cmd.Args[:1], args)
		}
		if err := checkBatchShimArgs(goos, cmd.Path, cmd.Args[1:]); err != nil {
			cmd.Err = err
		}
	}
}

// needsSpill reports whether cmd.exe would fail on args: a line break in one
// of them, or a command line over batchShimCmdLineBudget (each argument counts
// its two quotes and the separating space).
func needsSpill(args []string) bool {
	total := 0
	for _, a := range args {
		if strings.ContainsAny(a, "\r\n") {
			return true
		}
		total += len(a) + 3
	}
	return total > batchShimCmdLineBudget
}

// spillSystemPromptArgs returns args with each --system-prompt /
// --append-system-prompt value moved to a file made by spill and the flag
// swapped for its --…-file form (same replace/append semantics). Order and
// every other argument are kept; args itself is not modified.
func spillSystemPromptArgs(args []string, spill SpillFunc) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		var name string
		switch args[i] {
		case "--system-prompt":
			name = "system-prompt.md"
		case "--append-system-prompt":
			name = "append-system-prompt.md"
		}
		if name == "" || i+1 >= len(args) {
			out = append(out, args[i])
			continue
		}
		path, err := spill(name, []byte(args[i+1]))
		if err != nil {
			return nil, fmt.Errorf("claude: move %s off the cmd.exe command line: %w", args[i], err)
		}
		out = append(out, args[i]+"-file", path)
		i++
	}
	return out, nil
}

// isBatchShim reports whether path is a Windows batch file, which the OS
// runs through cmd.exe rather than directly.
func isBatchShim(goos, path string) bool {
	if goos != "windows" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cmd" || ext == ".bat"
}

// preferNativeBinary returns the native executable that a Windows npm shim
// wraps, or path unchanged when path is not such a shim.
//
// `npm i -g @anthropic-ai/claude-code` puts a `claude.cmd` on PATH whose only
// job is to run `claude.exe` next to it. Spawned through that shim every
// argument travels through cmd.exe, which ends the command line at the first
// line break: the multi-line --append-system-prompt iterion always passes cut
// the argv short and every flag after it — --verbose among them — was lost,
// so the CLI died with "stream-json requires --verbose". The native binary is
// started by CreateProcess directly: no line-break truncation, and the
// 32767-character limit rather than cmd.exe's 8191.
func preferNativeBinary(goos, path string) string {
	if !isBatchShim(goos, path) {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return path
	}
	if native, ok := nativeBinaryFromShim(filepath.Dir(path), string(data)); ok {
		return native
	}
	return path
}

// nativeBinaryFromShim extracts the executable an npm `.cmd` shim runs.
//
// The shim's invocation line has the shape
//
//	"%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*
//
// (`%~dp0` in some npm versions). Only that shape is accepted — the quoted
// first token of a line that forwards `%*` — so the `node.exe` probe of the
// node-based shim, or a line that merely mentions a path, is never mistaken
// for the target. The result must stay lexically inside dir (the shim already
// runs it, so honouring it widens nothing, but a `..` hop is refused) and exist.
func nativeBinaryFromShim(dir, shim string) (string, bool) {
	for _, line := range strings.Split(shim, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		end := strings.Index(line[1:], `"`)
		if end < 0 {
			continue
		}
		// Nothing but the argument forward may follow the quoted token: a
		// shim that adds flags or env of its own would lose them if replaced
		// by the bare binary.
		if strings.TrimSpace(line[end+2:]) != "%*" {
			continue
		}
		tok := line[1 : 1+end]
		low := strings.ToLower(tok)
		var rest string
		switch {
		case strings.HasPrefix(low, "%dp0%"):
			rest = tok[len("%dp0%"):]
		case strings.HasPrefix(low, "%~dp0"):
			rest = tok[len("%~dp0"):]
		default:
			continue
		}
		rest = strings.ReplaceAll(rest, `\`, "/")
		rest = strings.TrimLeft(rest, "/")
		if rest == "" || !strings.HasSuffix(strings.ToLower(rest), ".exe") ||
			strings.ContainsAny(rest, "%\r\n") {
			continue
		}
		parts := strings.Split(rest, "/")
		clean := true
		for _, p := range parts {
			if p == ".." || p == "" {
				clean = false
			}
		}
		if !clean {
			continue
		}
		native := filepath.Join(dir, filepath.Join(parts...))
		if info, err := os.Stat(native); err == nil && !info.IsDir() {
			return native, true
		}
	}
	return "", false
}

// checkBatchShimArgs refuses a spawn that cmd.exe would silently mangle:
// a batch-file CLI path with a line break in any argument. cmd.exe cuts the
// command line there, dropping the rest of that argument and every flag after
// it, so the CLI starts with a different configuration than the one asked for
// (or, as with --verbose, fails with an error that names the wrong culprit).
func checkBatchShimArgs(goos, cliPath string, args []string) error {
	if !isBatchShim(goos, cliPath) {
		return nil
	}
	for i, a := range args {
		if !strings.ContainsAny(a, "\r\n") {
			continue
		}
		flag := ""
		if i > 0 {
			flag = args[i-1]
		}
		return fmt.Errorf("claude: %s is a Windows batch shim and the argument after %q contains a line break that cannot be moved to a file; cmd.exe would cut the command line there and silently drop the flags that follow. Point the CLI path at the native claude.exe (for an npm install: <npm prefix>/node_modules/@anthropic-ai/claude-code/bin/claude.exe)", cliPath, flag)
	}
	return nil
}
