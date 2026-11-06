package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
// for the target. The result must stay inside dir (the shim already runs it,
// so honouring it widens nothing, but a `..` hop is refused) and exist.
func nativeBinaryFromShim(dir, shim string) (string, bool) {
	for _, line := range strings.Split(shim, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) || !strings.Contains(line, "%*") {
			continue
		}
		end := strings.Index(line[1:], `"`)
		if end < 0 {
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
		return fmt.Errorf("claude: %s is a Windows batch shim and the argument after %q contains a line break; cmd.exe would cut the command line there and silently drop the flags that follow. Point the CLI path at the native claude.exe (for an npm install: <npm prefix>/node_modules/@anthropic-ai/claude-code/bin/claude.exe)", cliPath, flag)
	}
	return nil
}
