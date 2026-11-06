package hostshell

import (
	"errors"
	"strings"
	"testing"
)

func withHead(t *testing.T, head string) {
	t.Helper()
	old := readHead
	readHead = func(string) string { return head }
	t.Cleanup(func() { readHead = old })
}

func TestFileCommandPs1OnWindowsUsesFile(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "powershell"), "")
	withHead(t, "")
	argv, sh, err := fileCommand(`C:\proj\prepare.ps1`, "", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Kind != KindPowerShell || sh.Source != SourceExtension {
		t.Fatalf("shell = %+v", sh)
	}
	joined := strings.Join(argv, " ")
	body := argv[len(argv)-1]
	// Run as `& '<file>'` under -Command, with the epilogue that keeps a failing
	// last program's exit code (-File would not).
	if !strings.Contains(joined, "-Command") || strings.Contains(joined, "-File") ||
		!strings.Contains(body, `& 'C:\proj\prepare.ps1'`) || !strings.Contains(body, "$LASTEXITCODE") {
		t.Fatalf("argv = %q", argv)
	}
}

func TestFileCommandShOnWindowsUsesSh(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "sh"), "")
	withHead(t, "echo hi\n")
	argv, sh, err := fileCommand(`C:\proj\prepare.sh`, "", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Kind != KindSh || sh.Source != SourceExtension {
		t.Fatalf("shell = %+v", sh)
	}
	if len(argv) != 2 || !strings.Contains(argv[0], "sh") || argv[1] != `C:\proj\prepare.sh` {
		t.Fatalf("argv = %q", argv)
	}
}

func TestFileCommandShWithoutAnyPosixShellIsNotFound(t *testing.T) {
	withSeams(t, fakeLook("pwsh"), "")
	withHead(t, "")
	_, _, err := fileCommand(`C:\proj\prepare.sh`, "", "windows")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
}

func TestFileCommandUnknownExtensionOnWindowsIsAnError(t *testing.T) {
	withSeams(t, fakeLook("pwsh"), "")
	withHead(t, "plain text\n")
	_, _, err := fileCommand(`C:\proj\prepare.txt`, "", "windows")
	var fe *FileInterpreterError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want *FileInterpreterError", err)
	}
	if !strings.Contains(err.Error(), "shell:") {
		t.Errorf("error %q does not name the remedy", err)
	}
}

func TestFileCommandShebangBeatsExtension(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "bash"), "")
	withHead(t, "#!/usr/bin/env bash\necho hi\n")
	argv, sh, err := fileCommand(`C:\proj\run.txt`, "", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Kind != KindBash || sh.Source != SourceShebang {
		t.Fatalf("shell = %+v", sh)
	}
	if argv[len(argv)-1] != `C:\proj\run.txt` {
		t.Fatalf("argv = %q", argv)
	}
}

func TestFileCommandExplicitShellWins(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "sh"), "")
	withHead(t, "#!/bin/sh\n")
	argv, sh, err := fileCommand(`C:\proj\prepare.sh`, "pwsh", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Kind != KindPowerShell || sh.Source != SourceExplicit {
		t.Fatalf("shell = %+v", sh)
	}
	if joined := strings.Join(argv, " "); !strings.Contains(joined, "-Command") || !strings.Contains(joined, `prepare.sh`) {
		t.Fatalf("argv = %q", argv)
	}
}

func TestFileCommandExplicitShellNotFound(t *testing.T) {
	withSeams(t, fakeLook("pwsh"), "")
	_, _, err := fileCommand(`C:\proj\prepare.sh`, "zsh", "windows")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
}

func TestFileCommandPythonOnWindows(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "py"), "")
	withHead(t, "")
	argv, sh, err := fileCommand(`C:\proj\seed.py`, "", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sh.Path, `\py.`) || argv[len(argv)-1] != `C:\proj\seed.py` {
		t.Fatalf("argv = %q shell = %+v", argv, sh)
	}
}

func TestFileCommandPosixQuotesThePath(t *testing.T) {
	withSeams(t, fakeLook(), "")
	argv, sh, err := fileCommand("/work/my dir/prepare.sh", "", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Path != "sh" {
		t.Fatalf("shell = %+v", sh)
	}
	if got := strings.Join(argv, "|"); got != "sh|-lc|'/work/my dir/prepare.sh'" {
		t.Fatalf("argv = %q", got)
	}
	q := posixQuote("it's")
	if q != `'it'\''s'` {
		t.Fatalf("posixQuote = %s", q)
	}
}

func TestShebangInterpreter(t *testing.T) {
	cases := map[string]string{
		"#!/bin/sh\n":                       "sh",
		"#!/usr/bin/env python3\nprint()\n": "python3",
		"#!/usr/bin/env -S node --x\n":      "node",
		"#! /bin/bash -e\n":                 "bash",
	}
	for head, want := range cases {
		got, ok := shebangInterpreter(head)
		if !ok || got != want {
			t.Errorf("shebangInterpreter(%q) = %q, %v; want %q", head, got, ok, want)
		}
	}
	for _, head := range []string{"", "echo hi\n", "#!\n", "# comment\n"} {
		if got, ok := shebangInterpreter(head); ok {
			t.Errorf("shebangInterpreter(%q) = %q, want none", head, got)
		}
	}
}

// An explicit POSIX shell on a POSIX host runs the file as it always did:
// `<shell> -lc '<path>'`, so the login PATH and the file's shebang still apply.
func TestFileCommandExplicitPosixShellKeepsLoginAndShebang(t *testing.T) {
	withSeams(t, fakeLook(), "")
	argv, sh, err := fileCommand("/work/my dir/run.sh", "bash", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Source != SourceExplicit {
		t.Fatalf("shell = %+v", sh)
	}
	if got := strings.Join(argv, "|"); got != "bash|-lc|'/work/my dir/run.sh'" {
		t.Fatalf("argv = %q", got)
	}
}

// ITERION_SHELL=pwsh on a POSIX host: the file still runs, as `& '<file>'`.
func TestFileCommandPowerShellOnPosixRunsTheFile(t *testing.T) {
	withSeams(t, fakeLook("pwsh"), "pwsh")
	argv, sh, err := fileCommand("/work/run.ps1", "", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Kind != KindPowerShell || !strings.Contains(argv[len(argv)-1], "& '/work/run.ps1'") {
		t.Fatalf("argv = %q shell = %+v", argv, sh)
	}
}
