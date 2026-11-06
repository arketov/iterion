package hostshell

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileInterpreterError says a script FILE was given to a Windows host that has
// no way to tell which interpreter runs it: a PowerShell command that names a
// `.sh` (or an unknown extension) hands it to the file association, which
// reports success without running anything.
type FileInterpreterError struct {
	Path string
	Ext  string
}

func (e *FileInterpreterError) Error() string {
	what := "a file with no extension"
	if e.Ext != "" {
		what = fmt.Sprintf("a %q file", e.Ext)
	}
	return fmt.Sprintf("hook path %s: Windows cannot run %s by itself and it has no `#!` line: "+
		"set `shell:` to its interpreter (sh, bash, pwsh, python, node …), name it by a known extension "+
		"(.ps1 .sh .bash .py .js .exe .bat .cmd), or use `script:` instead of `path:`", e.Path, what)
}

// shebangHeadBytes is how much of a script file is read to find its `#!` line.
const shebangHeadBytes = 256

// readHead returns the start of the file at path ("" when it cannot be read).
// A seam, replaced by tests.
var readHead = func(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, shebangHeadBytes)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n])
}

// FileCommand returns the argv that runs the script FILE at path, and the
// shell (interpreter) behind it.
//
// Order of choice — an explicit choice of the operator always wins:
//
//  1. shellName (a hook's `shell:`): that interpreter runs the file.
//  2. On Windows, the file's `#!` line (`#!/usr/bin/env python3` → python3).
//  3. On Windows, the file's extension: .ps1 PowerShell, .sh sh (else bash),
//     .bash bash, .py python, .js/.mjs node, .exe/.com/.bat/.cmd the file itself.
//  4. Anything else: on POSIX the file is run through the host shell as a
//     command (its shebang and executable bit decide, as they always did,
//     the path quoted so a space in it cannot split it); on Windows a
//     *FileInterpreterError, never a silent success.
//
// ITERION_SHELL does not pick the interpreter of a file with a known one: it
// is the default for command TEXT, not a statement about this file.
func FileCommand(path, shellName string) (argv []string, sh Shell, err error) {
	return fileCommand(path, shellName, goos)
}

func fileCommand(path, shellName, platform string) ([]string, Shell, error) {
	if name := strings.TrimSpace(shellName); name != "" && !strings.EqualFold(name, "auto") {
		sh, err := named(name, platform)
		if err != nil {
			return nil, Shell{}, err
		}
		sh.Source = SourceExplicit
		argv, err := fileArgv(sh, path, platform)
		return argv, sh, err
	}
	if platform != "windows" {
		sh, err := resolve(Request{}, platform)
		if err != nil {
			return nil, Shell{}, err
		}
		argv, err := fileArgv(sh, path, platform)
		return argv, sh, err
	}
	if interp, ok := shebangInterpreter(readHead(path)); ok {
		sh, err := interpreterShell(interp, platform)
		if err != nil {
			return nil, Shell{}, err
		}
		sh.Source = SourceShebang
		argv, err := fileArgv(sh, path, platform)
		return argv, sh, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	var sh Shell
	var err error
	switch ext {
	case ".ps1":
		sh, err = defaultShell(Request{}, platform)
	case ".sh":
		sh, err = interpreterShell("sh", platform)
	case ".bash":
		sh, err = interpreterShell("bash", platform)
	case ".py":
		sh, err = interpreterShell("python", platform)
	case ".js", ".mjs":
		sh, err = interpreterShell("node", platform)
	case ".exe", ".com", ".bat", ".cmd":
		return []string{path}, Shell{Kind: KindSh, Path: path, Source: SourceExtension}, nil
	default:
		return nil, Shell{}, &FileInterpreterError{Path: path, Ext: ext}
	}
	if err != nil {
		return nil, Shell{}, err
	}
	sh.Source = SourceExtension
	argv, err := fileArgv(sh, path, platform)
	return argv, sh, err
}

// fileArgv is the argv that makes sh run the file at path.
//
// PowerShell runs it through `-Command "& '<file>'"` with the same prelude and
// epilogue an inline hook gets, not `-File`: a script whose last statement is a
// failing program (`git clone …`, no `exit`) leaves `-File` with exit code 0,
// and a seed that failed would read as one that succeeded. `-ExecutionPolicy
// Bypass` is what lets the .ps1 run at all. A POSIX shell on a POSIX host keeps
// what a path: hook always did — the path, quoted, as the `-lc` command, so the
// file's shebang and executable bit decide. On Windows a POSIX shell is given
// the file as its script argument.
func fileArgv(sh Shell, path, platform string) ([]string, error) {
	switch {
	case sh.Kind == KindPowerShell:
		argv := append([]string{sh.Path}, psFlags...)
		quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
		return append(argv, "-ExecutionPolicy", "Bypass", "-Command", psPrelude+"& "+quoted+psEpilogue), nil
	case platform != "windows":
		argv, _ := sh.Argv(posixQuote(path), true)
		return argv, nil
	}
	return []string{sh.Path, path}, nil
}

// shebangInterpreter reads the interpreter name out of a `#!` first line:
// `#!/bin/sh` → sh, `#!/usr/bin/env python3` → python3. ok is false when head
// has no `#!` line or names no interpreter.
func shebangInterpreter(head string) (name string, ok bool) {
	if !strings.HasPrefix(head, "#!") {
		return "", false
	}
	line, _, _ := strings.Cut(head, "\n")
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(fields) == 0 {
		return "", false
	}
	base := func(s string) string {
		return strings.ToLower(filepath.Base(strings.ReplaceAll(s, `\`, "/")))
	}
	name = base(fields[0])
	if name == "env" {
		name = ""
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") || strings.Contains(f, "=") {
				continue // env's own flags (-S) and NAME=value assignments
			}
			name = base(f)
			break
		}
	}
	if name == "" || name == "." || name == "/" {
		return "", false
	}
	return name, true
}

// interpreterShell finds the interpreter called name on this host. A POSIX
// shell is looked up past the WSL launcher in System32; a language runtime is
// looked up under its usual names (python: python3, python, py on Windows).
func interpreterShell(name, platform string) (Shell, error) {
	switch strings.TrimSuffix(name, ".exe") {
	case "pwsh", "powershell":
		return defaultShell(Request{}, platform)
	case "sh", "dash", "ash", "zsh", "busybox":
		return posixShellNamed(append([]string{name}, "sh", "bash"))
	case "bash":
		return posixShellNamed([]string{"bash", "sh"})
	case "python", "python3", "python2":
		// On Windows `python3` is often the Microsoft Store stub (it answers
		// 9009 until the app is installed), so the launcher and `python` go first.
		names := []string{"python3", "python"}
		if platform == "windows" {
			names = []string{"py", "python", "python3"}
		}
		return firstFound(names)
	}
	return firstFound([]string{name})
}

func posixShellNamed(names []string) (Shell, error) {
	var looked []string
	for _, n := range names {
		looked = append(looked, n)
		p, err := lookPath(n)
		if err != nil || isWSLLauncher(p) {
			continue
		}
		kind := KindSh
		if n == "bash" {
			kind = KindBash
		}
		return Shell{Kind: kind, Path: p}, nil
	}
	return Shell{}, &NotFoundError{Looked: looked, Requested: looked[0], goos: goos}
}

func firstFound(names []string) (Shell, error) {
	for _, n := range names {
		if p, err := lookPath(n); err == nil {
			return Shell{Kind: KindSh, Path: p}, nil
		}
	}
	return Shell{}, &NotFoundError{Looked: names, Requested: names[0], goos: goos}
}

// posixQuote single-quotes a word for sh.
func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
