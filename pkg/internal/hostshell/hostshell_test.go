package hostshell

import (
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
	if err != nil || !ok || ext != ".ps1" || argv[len(argv)-1] != "-File" {
		t.Fatalf("windows default: %q %s %v", argv, ext, err)
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
