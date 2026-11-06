package proc

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rawExecAllowed lists the production files that may still call os/exec's
// constructors directly, each with the reason a console window is not the
// problem there. Everything else must start its child through [Command] /
// [CommandContext], which keeps a console-less Windows host (the desktop app
// and the studio daemon behind it) from opening a visible console for every
// git, hook, claude or node process it starts.
var rawExecAllowed = map[string]string{
	"pkg/cli/studio.go":             "opens the browser: rundll32 / open / xdg-open start a GUI program, not a console one",
	"pkg/runshell/spawn.go":         "a PTY-backed login shell (creack/pty, Unix only); Windows has a stub",
	"pkg/cli/schedule.go":           "crontab is a POSIX scheduler; the Windows build never reaches it",
	"pkg/backend/detect/detect.go":  "the only raw call is /usr/bin/security, a macOS keychain read",
	"pkg/internal/proc/nowindow.go": "the seam itself",
}

var rawExecCall = regexp.MustCompile(`\bexec\.Command(Context)?\(`)

// TestNoRawExecCommand keeps the no-window default from eroding: a new
// shell-out written as exec.Command in production code would bring the
// terminal burst back on Windows without any test failing.
func TestNoRawExecCommand(t *testing.T) {
	root := filepath.Join("..", "..") // pkg/internal/proc -> pkg
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := "pkg/" + filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if _, ok := rawExecAllowed[rel]; ok {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- walks this repository's own source tree
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j]
			}
			if rawExecCall.MatchString(code) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("raw exec.Command / exec.CommandContext in production code — use proc.Command / proc.CommandContext "+
			"(pkg/internal/proc) so the child gets no console window on Windows, or add the file to rawExecAllowed with the reason:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
