package claudesdk

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The real npm shim of @anthropic-ai/claude-code on Windows (same shape).
const npmNativeShim = "@ECHO off\r\n" +
	"GOTO start\r\n" +
	":find_dp0\r\n" +
	"SET dp0=%~dp0\r\n" +
	"EXIT /b\r\n" +
	":start\r\n" +
	"SETLOCAL\r\n" +
	"CALL :find_dp0\r\n" +
	"\r\n" +
	"\"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\bin\\claude.exe\"   %*\r\n"

// The node-based shim npm generates for a JS package: it probes for a
// node.exe next to itself and runs cli.js. There is no native binary to hand
// the spawn, and node.exe must never be mistaken for one.
const npmNodeShim = "@ECHO off\r\n" +
	"SETLOCAL\r\n" +
	"CALL :find_dp0\r\n" +
	"IF EXIST \"%dp0%\\node.exe\" (\r\n" +
	"  SET \"_prog=%dp0%\\node.exe\"\r\n" +
	") ELSE (\r\n" +
	"  SET \"_prog=node\"\r\n" +
	")\r\n" +
	"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\cli.js\" %*\r\n"

func writeShimFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The live bug: `claude` on a Windows npm install resolves to claude.cmd, and
// the spawn must go to the claude.exe it wraps instead of through cmd.exe.
func TestPreferNativeBinary_NpmShimResolvesToNativeExe(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude.cmd")
	exe := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	writeShimFile(t, shim, npmNativeShim)
	writeShimFile(t, exe, "MZ")

	if got := preferNativeBinary("windows", shim); got != exe {
		t.Errorf("preferNativeBinary = %q, want %q", got, exe)
	}
	if !isBatchShim("windows", filepath.Join(dir, "CLAUDE.CMD")) {
		t.Error("upper-case .CMD must be recognised as a batch shim")
	}
}

func TestPreferNativeBinary_LeavesEverythingElseAlone(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude.cmd")
	writeShimFile(t, shim, npmNativeShim)
	writeShimFile(t, filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"), "MZ")

	// Not Windows: a .cmd is just a file name.
	if got := preferNativeBinary("linux", shim); got != shim {
		t.Errorf("linux: got %q, want the path unchanged", got)
	}
	// Not a batch shim.
	native := filepath.Join(dir, "claude.exe")
	writeShimFile(t, native, "MZ")
	if got := preferNativeBinary("windows", native); got != native {
		t.Errorf("exe: got %q, want the path unchanged", got)
	}
	// Shim whose target is missing on disk.
	missing := filepath.Join(t.TempDir(), "claude.cmd")
	writeShimFile(t, missing, npmNativeShim)
	if got := preferNativeBinary("windows", missing); got != missing {
		t.Errorf("missing target: got %q, want the shim unchanged", got)
	}
	// Node-based shim: node.exe is never the answer.
	nodeDir := t.TempDir()
	nodeShim := filepath.Join(nodeDir, "claude.cmd")
	writeShimFile(t, nodeShim, npmNodeShim)
	writeShimFile(t, filepath.Join(nodeDir, "node.exe"), "MZ")
	if got := preferNativeBinary("windows", nodeShim); got != nodeShim {
		t.Errorf("node shim: got %q, want the shim unchanged", got)
	}
}

func TestNativeBinaryFromShim_AcceptsDp0Spellings(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "claude.exe")
	writeShimFile(t, exe, "MZ")
	for _, shim := range []string{
		"\"%dp0%\\bin\\claude.exe\" %*\r\n",
		"\"%~dp0\\bin\\claude.exe\" %*\n",
		"\"%DP0%/bin/claude.exe\"  %*",
	} {
		if got, ok := nativeBinaryFromShim(dir, shim); !ok || got != exe {
			t.Errorf("shim %q: got (%q, %v), want (%q, true)", shim, got, ok, exe)
		}
	}
}

func TestNativeBinaryFromShim_RefusesEscapesAndNonInvocations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "npm")
	writeShimFile(t, filepath.Join(root, "evil.exe"), "MZ")
	writeShimFile(t, filepath.Join(dir, "claude.exe"), "MZ")
	for name, shim := range map[string]string{
		"parent hop":      "\"%dp0%\\..\\evil.exe\" %*\r\n",
		"env expansion":   "\"%dp0%\\%OTHER%\\claude.exe\" %*\r\n",
		"no %*":           "\"%dp0%\\claude.exe\"\r\n",
		"extra flag":      "\"%dp0%\\claude.exe\" --flag %*\r\n",
		"not first token": "SET X=1 & \"%dp0%\\claude.exe\" %*\r\n",
		"not an exe":      "\"%dp0%\\claude.js\" %*\r\n",
		"absolute":        "\"C:\\Windows\\evil.exe\" %*\r\n",
	} {
		if got, ok := nativeBinaryFromShim(dir, shim); ok {
			t.Errorf("%s: accepted %q, want refusal", name, got)
		}
	}
}

// The path that lost --verbose: argv through a batch file with a multi-line
// system prompt. Reproduced on Windows (Go 1.27, cmd.exe /c): the child sees
// the arguments up to the first line break and nothing after it. The spawn is
// refused up front instead of running a silently different command.
func TestCheckBatchShimArgs_RefusesLineBreakThroughCmdShim(t *testing.T) {
	args := buildArgs(processConfig{AppendSystemPrompt: "Ты планировщик.\n- rule"}, true)

	err := checkBatchShimArgs("windows", `C:\npm\claude.cmd`, args)
	if err == nil {
		t.Fatal("a line break in an argument of a .cmd shim must be refused")
	}
	for _, want := range []string{"claude.cmd", "--append-system-prompt", "claude.exe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if checkBatchShimArgs("windows", `C:\npm\claude.BAT`, args) == nil {
		t.Error(".BAT must be refused too")
	}
}

func TestCheckBatchShimArgs_AllowsEverythingElse(t *testing.T) {
	multiline := buildArgs(processConfig{AppendSystemPrompt: "a\nb"}, true)
	flat := buildArgs(processConfig{AppendSystemPrompt: "a b", Model: "m"}, true)

	if err := checkBatchShimArgs("windows", `C:\claude\claude.exe`, multiline); err != nil {
		t.Errorf("native exe takes line breaks fine: %v", err)
	}
	if err := checkBatchShimArgs("linux", "/usr/bin/claude.cmd", multiline); err != nil {
		t.Errorf("not Windows: %v", err)
	}
	if err := checkBatchShimArgs("windows", `C:\npm\claude.cmd`, flat); err != nil {
		t.Errorf("single-line args through a shim are intact: %v", err)
	}
}

// The production path: the claude_code backend builds its host command itself
// (a CommandBuilder), so exec.CommandContext(ctx, "claude") has already
// resolved PATHEXT to claude.cmd and the SDK's own spawn checks never run.
// AdaptHostCmd is what protects that command.
func TestAdaptHostCmd_SwapsNpmShimForNativeExe(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude.cmd")
	exe := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	writeShimFile(t, shim, npmNativeShim)
	writeShimFile(t, exe, "MZ")

	cmd := &exec.Cmd{Path: shim, Args: []string{"claude", "--append-system-prompt", "a\nb", "--verbose"}}
	adaptHostCmd("windows", cmd, nil)
	if cmd.Path != exe {
		t.Errorf("Path = %q, want %q", cmd.Path, exe)
	}
	if cmd.Err != nil {
		t.Errorf("Err = %v, want nil: a resolvable shim is fine with line breaks", cmd.Err)
	}
	if len(cmd.Args) != 4 || cmd.Args[0] != "claude" {
		t.Errorf("Args changed: %v", cmd.Args)
	}
}

func TestAdaptHostCmd_UnresolvableShimWithLineBreakFailsStart(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude.cmd")
	writeShimFile(t, shim, npmNodeShim)

	cmd := &exec.Cmd{Path: shim, Args: []string{"claude", "--append-system-prompt", "a\nb"}}
	adaptHostCmd("windows", cmd, nil)
	if cmd.Err == nil {
		t.Fatal("cmd.Err must carry the refusal so Start fails")
	}
	if cmd.Path != shim {
		t.Errorf("Path = %q, want the shim unchanged", cmd.Path)
	}

	flat := &exec.Cmd{Path: shim, Args: []string{"claude", "--print"}}
	adaptHostCmd("windows", flat, nil)
	if flat.Err != nil {
		t.Errorf("single-line args through a shim are intact: %v", flat.Err)
	}
}

func TestAdaptHostCmd_NoOpElsewhere(t *testing.T) {
	adaptHostCmd("windows", nil, nil)
	for _, c := range []struct {
		goos, path string
	}{
		{"linux", "/usr/bin/claude.cmd"},
		{"windows", `C:\claude\claude.exe`},
	} {
		cmd := &exec.Cmd{Path: c.path, Args: []string{"claude", "a\nb"}}
		adaptHostCmd(c.goos, cmd, nil)
		if cmd.Path != c.path || cmd.Err != nil {
			t.Errorf("%s %s: changed (Path=%q Err=%v)", c.goos, c.path, cmd.Path, cmd.Err)
		}
	}
	pre := &exec.Cmd{Path: `C:\npm\claude.cmd`, Args: []string{"claude", "a\nb"}, Err: os.ErrNotExist}
	adaptHostCmd("windows", pre, nil)
	if pre.Err != os.ErrNotExist {
		t.Error("an Err already set by LookPath must be left as is")
	}
}

// recordingSpill is a SpillFunc that keeps what it was asked to write and
// answers with a fixed Windows-looking path per name.
type recordingSpill struct {
	files map[string]string
	calls int
}

func (r *recordingSpill) spill(name string, content []byte) (string, error) {
	r.calls++
	if r.files == nil {
		r.files = map[string]string{}
	}
	r.files[name] = string(content)
	return `C:\tmp\` + name, nil
}

func nodeShimInDir(t *testing.T) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "claude.cmd")
	writeShimFile(t, shim, npmNodeShim)
	return shim
}

func claudeArgv(cfg processConfig) []string {
	return append([]string{"claude"}, buildArgs(cfg, true)...)
}

// A node-based shim cannot be swapped for a native exe, so the multi-line
// system prompt must leave argv through a file instead of failing the start.
func TestAdaptHostCmd_UnresolvableShimSpillsMultilineSystemPrompt(t *testing.T) {
	shim := nodeShimInDir(t)
	prompt := "Ты планировщик.\n- rule"
	argv := claudeArgv(processConfig{AppendSystemPrompt: prompt, Model: "m"})
	cmd := &exec.Cmd{Path: shim, Args: append([]string(nil), argv...)}
	rec := &recordingSpill{}

	adaptHostCmd("windows", cmd, rec.spill)

	if cmd.Err != nil {
		t.Fatalf("Err = %v, want nil: the prompt leaves argv through a file", cmd.Err)
	}
	want := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--append-system-prompt" {
			want = append(want, "--append-system-prompt-file", `C:\tmp\append-system-prompt.md`)
			i++
			continue
		}
		want = append(want, argv[i])
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("Args = %q, want %q", cmd.Args, want)
	}
	if got := rec.files["append-system-prompt.md"]; got != prompt {
		t.Errorf("spilled content = %q, want %q", got, prompt)
	}
	if cmd.Path != shim {
		t.Errorf("Path = %q, want the shim unchanged", cmd.Path)
	}
}

func TestAdaptHostCmd_UnresolvableShimSpillsOversizedSingleLinePrompt(t *testing.T) {
	shim := nodeShimInDir(t)

	big := strings.Repeat("x", 9000)
	cmd := &exec.Cmd{Path: shim, Args: claudeArgv(processConfig{AppendSystemPrompt: big})}
	rec := &recordingSpill{}
	adaptHostCmd("windows", cmd, rec.spill)
	if cmd.Err != nil || rec.files["append-system-prompt.md"] != big {
		t.Errorf("oversized prompt not spilled (Err=%v, files=%d)", cmd.Err, len(rec.files))
	}
	if strings.Contains(strings.Join(cmd.Args, " "), big) {
		t.Error("the oversized prompt must be gone from argv")
	}

	small := claudeArgv(processConfig{AppendSystemPrompt: strings.Repeat("x", 100)})
	short := &exec.Cmd{Path: shim, Args: append([]string(nil), small...)}
	rec = &recordingSpill{}
	adaptHostCmd("windows", short, rec.spill)
	if rec.calls != 0 || !reflect.DeepEqual(short.Args, small) || short.Err != nil {
		t.Errorf("a short one-line prompt must stay on argv (calls=%d Err=%v Args=%q)", rec.calls, short.Err, short.Args)
	}
}

func TestAdaptHostCmd_UnresolvableShimSpillsSystemPromptFlagAndRejectsLeftovers(t *testing.T) {
	shim := nodeShimInDir(t)
	rec := &recordingSpill{}
	cmd := &exec.Cmd{Path: shim, Args: []string{"claude", "--system-prompt", "a\nb", "--verbose"}}
	adaptHostCmd("windows", cmd, rec.spill)
	want := []string{"claude", "--system-prompt-file", `C:\tmp\system-prompt.md`, "--verbose"}
	if cmd.Err != nil || !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("Err=%v Args=%q, want %q", cmd.Err, cmd.Args, want)
	}
	if rec.files["system-prompt.md"] != "a\nb" {
		t.Errorf("spilled = %q", rec.files["system-prompt.md"])
	}

	// A line break the spill does not cover is still refused.
	left := &exec.Cmd{Path: shim, Args: []string{"claude", "--append-system-prompt", "a\nb", "--json-schema", "{\n}"}}
	adaptHostCmd("windows", left, (&recordingSpill{}).spill)
	if left.Err == nil {
		t.Error("a multi-line --json-schema through a shim must still fail the start")
	}
}

func TestAdaptHostCmd_UnresolvableShimSpillErrorFailsStart(t *testing.T) {
	shim := nodeShimInDir(t)
	boom := errors.New("disk full")
	orig := []string{"claude", "--append-system-prompt", "a\nb", "--verbose"}
	cmd := &exec.Cmd{Path: shim, Args: append([]string(nil), orig...)}
	adaptHostCmd("windows", cmd, func(string, []byte) (string, error) { return "", boom })
	if !errors.Is(cmd.Err, boom) {
		t.Errorf("Err = %v, want it to wrap the spill error", cmd.Err)
	}
	if !reflect.DeepEqual(cmd.Args, orig) {
		t.Errorf("Args changed on a failed spill: %q", cmd.Args)
	}
}

func TestAdaptHostCmd_NativeSwapAndNonShimNeverSpill(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude.cmd")
	exe := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	writeShimFile(t, shim, npmNativeShim)
	writeShimFile(t, exe, "MZ")

	for _, c := range []struct{ goos, path string }{
		{"windows", shim},
		{"windows", `C:\claude\claude.exe`},
		{"linux", "/usr/bin/claude.cmd"},
	} {
		orig := []string{"claude", "--append-system-prompt", "a\nb"}
		cmd := &exec.Cmd{Path: c.path, Args: append([]string(nil), orig...)}
		rec := &recordingSpill{}
		adaptHostCmd(c.goos, cmd, rec.spill)
		if rec.calls != 0 || cmd.Err != nil || !reflect.DeepEqual(cmd.Args, orig) {
			t.Errorf("%s %s: spilled or changed (calls=%d Err=%v Args=%q)", c.goos, c.path, rec.calls, cmd.Err, cmd.Args)
		}
	}
}
