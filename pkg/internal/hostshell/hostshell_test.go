package hostshell

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func fakeLook(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return `C:\bin\` + name + ".exe", nil
			}
		}
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
}

func withSeams(t *testing.T, look func(string) (string, error), env string) {
	t.Helper()
	oldLook, oldEnv := lookPath, getenv
	lookPath = look
	getenv = func(string) string { return env }
	t.Cleanup(func() { lookPath, getenv = oldLook, oldEnv })
}

func TestPosixDefaultsUnchanged(t *testing.T) {
	withSeams(t, fakeLook(), "")
	sh, err := resolve(Request{}, "linux")
	if err != nil || sh.Path != "sh" || sh.Kind != KindSh {
		t.Fatalf("default = %+v, %v", sh, err)
	}
	argv, stdin := sh.Argv("echo hi", true)
	if strings.Join(argv, " ") != "sh -lc echo hi" || stdin != nil {
		t.Fatalf("argv = %q", argv)
	}
	b, _ := resolve(Request{PosixDefault: KindBash}, "darwin")
	argv, _ = b.Argv("echo hi", false)
	if strings.Join(argv, " ") != "bash -c echo hi" {
		t.Fatalf("argv = %q", argv)
	}
}

func TestWindowsPrefersPwshThenPowerShell(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "powershell"), "")
	sh, err := resolve(Request{}, "windows")
	if err != nil || sh.Kind != KindPowerShell || !strings.Contains(sh.Path, "pwsh") {
		t.Fatalf("got %+v, %v", sh, err)
	}
	withSeams(t, fakeLook("powershell"), "")
	sh, err = resolve(Request{}, "windows")
	if err != nil || !strings.Contains(sh.Path, "powershell") {
		t.Fatalf("got %+v, %v", sh, err)
	}
}

func TestWindowsNoShellIsTypedAndHelpful(t *testing.T) {
	withSeams(t, fakeLook(), "")
	_, err := resolve(Request{}, "windows")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatal("must satisfy exec.ErrNotFound")
	}
	for _, want := range []string{"pwsh", "powershell", EnvVar} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestExplicitNameAndEnvOverride(t *testing.T) {
	withSeams(t, fakeLook("bash"), "")
	sh, err := resolve(Request{Name: "bash"}, "windows")
	if err != nil || sh.Kind != KindBash {
		t.Fatalf("got %+v, %v", sh, err)
	}
	if _, err := resolve(Request{Name: "pwsh"}, "windows"); err == nil || !strings.Contains(err.Error(), `"pwsh"`) {
		t.Fatalf("missing explicit shell must name it: %v", err)
	}
	withSeams(t, fakeLook("pwsh"), "pwsh")
	sh, err = resolve(Request{}, "linux")
	if err != nil || sh.Kind != KindPowerShell {
		t.Fatalf("ITERION_SHELL ignored: %+v, %v", sh, err)
	}
	// An explicit Name beats the environment.
	sh, _ = resolve(Request{Name: "sh"}, "linux")
	if sh.Kind != KindSh {
		t.Fatalf("Name must win over env: %+v", sh)
	}
}

func TestPowerShellArgvInlineAndStreamsLongScripts(t *testing.T) {
	sh := Shell{Kind: KindPowerShell, Path: "pwsh"}
	argv, stdin := sh.Argv("Write-Output 'é'", true)
	if stdin != nil || argv[len(argv)-2] != "-Command" || !strings.Contains(argv[len(argv)-1], "Write-Output 'é'") {
		t.Fatalf("argv = %q", argv)
	}
	if strings.Contains(strings.Join(argv, " "), "-EncodedCommand") {
		t.Fatal("-EncodedCommand makes powershell.exe 5.1 write CLIXML to stderr")
	}
	long := strings.Repeat("a", psInlineLimit)
	argv, stdin = sh.Argv(long, false)
	if stdin == nil || argv[len(argv)-2] != "-Command" || argv[len(argv)-1] != "-" {
		t.Fatalf("long script not streamed: %q", argv)
	}
}

func TestEscapeSingleQuotedPowerShellQuotes(t *testing.T) {
	tests := []struct {
		name, platform, in, want string
	}{
		{"ascii apostrophe", "windows", "it's", "it''s"},
		{"right single quote", "windows", "a’b", "a’’b"},
		{"subexpression in typographic quotes", "windows", "‘$(Write-Output pwned)’", "‘‘$(Write-Output pwned)’’"},
		{"all four typographic quotes", "windows", "‘’‚‛", "‘‘’’‚‚‛‛"},
		{"ascii next to typographic", "windows", "'’", "''’’"},
		{"empty", "windows", "", ""},
		{"not single quotes", "windows", "Привет “q” \"d\"", "Привет “q” \"d\""},
		{"posix apostrophe", "linux", "it's", `it'\''s`},
		{"posix typographic quotes are plain bytes", "linux", "‘a’", "‘a’"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeSingleQuoted(tc.in, tc.platform); got != tc.want {
				t.Fatalf("escapeSingleQuoted(%q, %q) = %q, want %q", tc.in, tc.platform, got, tc.want)
			}
		})
	}
}

// A quoted value must reach a real PowerShell as text: its tokenizer closes a
// '...' literal on a lone U+2018..U+201B, and what follows would run as code.
func TestPowerShellLiteralRoundTripsTypographicQuotes(t *testing.T) {
	values := []string{
		"a’b",
		"‘$(Write-Output pwned)’",
		"x‚y‛z‘w",
		"it's ’",
	}
	ran := 0
	for _, name := range []string{"pwsh", "powershell"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ran++
		sh := Shell{Kind: KindPowerShell, Path: path}
		for _, v := range values {
			t.Run(name+"/"+v, func(t *testing.T) {
				script := "[Console]::Out.Write('" + escapeSingleQuoted(v, "windows") + "')"
				out, err := sh.Command(context.Background(), script, false).Output()
				if err != nil {
					t.Fatalf("run %q: %v", script, err)
				}
				if string(out) != v {
					t.Fatalf("got %q, want %q", out, v)
				}
			})
		}
	}
	if ran == 0 {
		t.Skip("no pwsh/powershell on PATH")
	}
}

func TestOutputForNormalisesCRLFOnlyForPowerShell(t *testing.T) {
	ps := exec.Command("pwsh", "-NoLogo", "-NoProfile")
	var got strings.Builder
	w := OutputFor(ps, &got)
	// A CR split from its LF by a Write boundary, a lone CR, and a CRLF.
	for _, chunk := range []string{"a\r", "\nb\r\nc\rd"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got.String() != "a\nb\nc\rd" {
		t.Fatalf("got %q", got.String())
	}
	var plain strings.Builder
	if OutputFor(exec.Command("sh", "-c", "x"), &plain) != any(&plain) {
		t.Fatal("a non-PowerShell stream must be passed through untouched")
	}
}

func TestScriptInterpreter(t *testing.T) {
	withSeams(t, fakeLook("pwsh"), "")
	argv, ext, ok, err := scriptInterpreter("", "windows")
	if err != nil || !ok || ext != ".ps1" || argv[len(argv)-1] != "-Command" || argv[1] != "-NoLogo" {
		t.Fatalf("windows default: %q %s %v", argv, ext, err)
	}
	if joined := strings.Join(argv, " "); !strings.Contains(joined, "-ExecutionPolicy Bypass") || strings.Contains(joined, "-File") {
		t.Fatalf("a script runs under -Command with the policy bypassed, never -File: %q", argv)
	}
	argv, ext, _, _ = scriptInterpreter("", "linux")
	if len(argv) != 1 || argv[0] != "sh" || ext != ".sh" {
		t.Fatalf("linux default changed: %q", argv)
	}
	if _, _, _, err := scriptInterpreter("sh", "windows"); err == nil || !strings.Contains(err.Error(), "powershell") {
		t.Fatalf("explicit sh on Windows without sh must explain: %v", err)
	}
	if _, _, ok, _ := scriptInterpreter("cobol", "linux"); ok {
		t.Fatal("unknown language accepted")
	}
	argv, _, _, _ = scriptInterpreter("py", "windows")
	if argv[0] != "python3" {
		t.Fatalf("python: %q", argv)
	}
}

func TestScriptFileContentAndArg(t *testing.T) {
	b := "Write-Output 'привет'\ngit status"
	if got := ScriptFileContent(".ps1", b); got != "\ufeff"+b+psEpilogue {
		t.Fatalf("ps1 content = %q", got)
	}
	for _, ext := range []string{".sh", ".py", ".js"} {
		if got := ScriptFileContent(ext, b); got != b {
			t.Fatalf("%s content changed: %q", ext, got)
		}
	}
	path := `C:\Users\o'brien\AppData\Local\Temp\.iterion-script-1.ps1`
	want := psPrelude + `& 'C:\Users\o''brien\AppData\Local\Temp\.iterion-script-1.ps1'` + psEpilogue
	if got := ScriptFileArg(".ps1", path); got != want {
		t.Fatalf("ps1 arg = %q, want %q", got, want)
	}
	if got := ScriptFileArg(".sh", "/tmp/x.sh"); got != "/tmp/x.sh" {
		t.Fatalf("sh arg = %q", got)
	}

	withSeams(t, fakeLook("powershell"), "")
	argv, ext, _, err := scriptInterpreter("powershell", "windows")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], append(argv[1:], ScriptFileArg(ext, path))...)
	if !IsPowerShell(cmd) || !strings.Contains(argv[0], "powershell") {
		t.Fatalf("the script command must still be recognised as PowerShell (CRLF to LF): %q", cmd.Args)
	}
}

func TestOutputsForKeepsASharedWriterShared(t *testing.T) {
	ps := exec.Command("pwsh", "-NoLogo", "-NoProfile")
	var shared, other strings.Builder
	o, e := OutputsFor(ps, &shared, &shared)
	if o != e {
		t.Fatal("one writer for both streams must stay one wrapper: os/exec serialises only identical Stdout and Stderr")
	}
	o, e = OutputsFor(ps, &shared, &other)
	if o == e {
		t.Fatal("different writers must stay different")
	}
}

func TestLooksPosix(t *testing.T) {
	posix := []string{
		"set -e\necho hi",
		"PROJECT_DIR=\"/x\"\ngit -C \"$PROJECT_DIR\" status",
		"export FOO=1",
		"#!/bin/sh\necho hi",
		"if [ -d x ]; then\n  echo y\nfi",
		"# comment first\nset -eu",
	}
	for _, s := range posix {
		if !LooksPosix(s) {
			t.Errorf("LooksPosix(%q) = false, want true", s)
		}
	}
	notPosix := []string{
		"Write-Output hi",
		"$projectDir = 'C:/x'\ngit -C $projectDir status",
		"git status",
		"git add -A && git commit -m x",
		"# just a comment",
		"$env:FOO = '1'",
		"set -Name x -Value 1",
		"$h = @{\n  Name='x'\n  Other='y'\n}\nWrite-Output $h",
		"$t = @\"\nKEY=value\nset -e\n\"@\nWrite-Output $t",
		"",
	}
	for _, s := range notPosix {
		if LooksPosix(s) {
			t.Errorf("LooksPosix(%q) = true, want false", s)
		}
	}
}

func TestLegacyShScriptOnAPowerShellHost(t *testing.T) {
	script := "set -e\necho hi"
	// sh on PATH: the old script keeps running under it, with a note.
	withSeams(t, fakeLook("pwsh", "sh"), "")
	sh, err := resolve(Request{Script: script}, "windows")
	if err != nil || sh.Kind != KindSh || sh.Note == "" {
		t.Fatalf("got %+v, %v; want sh with a note", sh, err)
	}
	// A PowerShell script is not touched.
	sh, _ = resolve(Request{Script: "Write-Output hi"}, "windows")
	if sh.Kind != KindPowerShell || sh.Note != "" {
		t.Fatalf("PowerShell text must stay on PowerShell: %+v", sh)
	}
	// An explicit shell wins over the sniffing.
	sh, _ = resolve(Request{Name: "pwsh", Script: script}, "windows")
	if sh.Kind != KindPowerShell {
		t.Fatalf("explicit shell ignored: %+v", sh)
	}
	// No sh anywhere: a clear, typed error — not PowerShell misreading sh.
	withSeams(t, fakeLook("pwsh"), "")
	_, err = resolve(Request{Script: script}, "windows")
	var pe *PosixScriptError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "shell:") {
		t.Fatalf("err = %v, want *PosixScriptError naming the remedy", err)
	}
	// POSIX hosts are unaffected.
	if sh, err := resolve(Request{Script: script}, "linux"); err != nil || sh.Kind != KindSh || sh.Note != "" {
		t.Fatalf("linux: %+v, %v", sh, err)
	}
}

func TestResolveReportsSource(t *testing.T) {
	withSeams(t, fakeLook("pwsh", "bash", "sh"), "")
	sh, err := resolve(Request{}, "windows")
	if err != nil || sh.Source != SourceDefault {
		t.Fatalf("default: %+v, %v", sh, err)
	}
	sh, err = resolve(Request{Name: "bash"}, "windows")
	if err != nil || sh.Source != SourceExplicit {
		t.Fatalf("explicit: %+v, %v", sh, err)
	}
	withSeams(t, fakeLook("pwsh", "bash"), "bash")
	sh, err = resolve(Request{}, "windows")
	if err != nil || sh.Source != SourceEnv {
		t.Fatalf("env: %+v, %v", sh, err)
	}
	withSeams(t, fakeLook("pwsh", "sh"), "")
	sh, err = resolve(Request{Script: "set -e\necho hi"}, "windows")
	if err != nil || sh.Kind != KindSh || sh.Source != SourcePosixScript {
		t.Fatalf("posix script: %+v, %v", sh, err)
	}
}

func TestRequestedShellAndName(t *testing.T) {
	withSeams(t, fakeLook(), "pwsh")
	if name, src := RequestedShell(Request{Name: "sh"}); name != "sh" || src != SourceExplicit {
		t.Fatalf("request name: %q %q", name, src)
	}
	if name, src := RequestedShell(Request{}); name != "pwsh" || src != SourceEnv {
		t.Fatalf("env: %q %q", name, src)
	}
	withSeams(t, fakeLook(), "auto")
	if name, src := RequestedShell(Request{}); name != "" || src != SourceDefault {
		t.Fatalf("auto: %q %q", name, src)
	}
	if got := (Shell{Path: `C:\Program Files\PowerShell\7\pwsh.exe`}).Name(); got != "pwsh" {
		t.Fatalf("Name = %q", got)
	}
}

func TestLooksPosixSkipsBlockCommentsAndNestedBraces(t *testing.T) {
	notPosix := []string{
		"<#\n.EXAMPLE\nNAME=value\n#>\nWrite-Output hi",
		"<# NAME=value #>\nWrite-Output hi",
		"$h = @{\n  F = {\n    1\n  }\n  Y=2\n}\nWrite-Output $h",
	}
	for _, s := range notPosix {
		if LooksPosix(s) {
			t.Errorf("LooksPosix(%q) = true, want false", s)
		}
	}
	if !LooksPosix("set -e\necho hi") {
		t.Error("set -e must still read as sh")
	}
	if !LooksPosix("<#\n.x\n#>\nset -e") {
		t.Error("an sh line after a block comment must still be read")
	}
}
