package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
	"github.com/arketov/iterion/pkg/internal/hostshelltest"
)

func TestEscapeDoubleQuotesForLegacyArgs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{``, ``},
		{`a"b`, `a\"b`},
		{`a\"b`, `a\\\"b`},
		{`a\\"b`, `a\\\\\"b`},
		{`a\b`, `a\b`},
		{`""`, `\"\"`},
		{`{"k":"v"}`, `{\"k\":\"v\"}`},
	} {
		if got := escapeDoubleQuotesForLegacyArgs(c.in); got != c.want {
			t.Errorf("escapeDoubleQuotesForLegacyArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLegacyNativeArgsRenderer(t *testing.T) {
	jsonVal := map[string]any{"k": "v"}

	legacyCalls := 0
	yes := func() bool { legacyCalls++; return true }
	no := func() bool { legacyCalls++; return false }

	on := legacyNativeArgsRenderer(shellEscapeValue, yes)
	if got, want := on(jsonVal, ShapeUndeclared), `'{\"k\":\"v\"}'`; got != want {
		t.Errorf("legacy render of a json value = %q, want %q", got, want)
	}
	if got, want := on([]string{`a"b`, "c"}, ShapeUndeclared), `'a\"b' 'c'`; got != want {
		t.Errorf("legacy render of a list = %q, want %q", got, want)
	}
	// The word barrier is the single quote, and it is untouched: only the
	// double quote gains a backslash.
	if got, want := on(`it's "x"`, ShapeUndeclared), shellEscape(`it's \"x\"`); got != want {
		t.Errorf("legacy render of a quote mix = %q, want %q", got, want)
	}

	off := legacyNativeArgsRenderer(shellEscapeValue, no)
	if got, want := off(jsonVal, ShapeUndeclared), shellEscapeValue(jsonVal, ShapeUndeclared); got != want {
		t.Errorf("non-legacy render = %q, want %q", got, want)
	}

	legacyCalls = 0
	if got, want := on("plain", ShapeUndeclared), shellEscapeValue("plain", ShapeUndeclared); got != want {
		t.Errorf("render without a quote = %q, want %q", got, want)
	}
	if legacyCalls != 0 {
		t.Errorf("legacy() was asked %d times for a value with no double quote", legacyCalls)
	}
}

func TestHostCommandValueRendererSandboxed(t *testing.T) {
	render := hostCommandValueRenderer(true)
	for _, v := range []any{`say "hi"`, map[string]any{"k": "v"}, []string{`a"b`}} {
		if got, want := render(v, ShapeUndeclared), shellEscapeValue(v, ShapeUndeclared); got != want {
			t.Errorf("sandboxed render of %v = %q, want %q", v, got, want)
		}
	}
}

// legacyArgsEchoEnv switches the test binary into an argv echoer: it prints
// what a native program receives after the marker argument.
const (
	legacyArgsEchoEnv    = "ITERION_TEST_ARGV_ECHO"
	legacyArgsEchoMarker = "ITERION-ARGV-FOLLOWS"
)

func TestLegacyArgsHelperProcess(t *testing.T) {
	if os.Getenv(legacyArgsEchoEnv) != "1" {
		return
	}
	for i, a := range os.Args {
		if a == legacyArgsEchoMarker {
			out, _ := json.Marshal(os.Args[i+1:])
			fmt.Println(string(out))
			os.Exit(0)
		}
	}
	os.Exit(3)
}

// A value with a double quote reaches a native program intact whatever the
// PowerShell's argument passing is: Windows PowerShell 5.1 and pwsh in Legacy
// mode drop an unescaped `"`, pwsh 7.3+ would double an escaped one. Run under
// ITERION_TEST_POWERSHELL=powershell for 5.1, by default for pwsh.
func TestLegacyArgsJSONReachesNativeProgram(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell argument passing is a Windows host concern")
	}
	hostshelltest.UsePowerShell(t)
	t.Setenv(legacyArgsEchoEnv, "1")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "injected")
	e := &ClawExecutor{}
	ref := &ir.Ref{Kind: ir.RefInput, Path: []string{"x"}, Raw: "{{input.x}}"}

	values := []string{
		`{"k":"v"}`,
		`{"k": "v w"}`,
		`say "hi"`,
		`a\"b`,
		`it's`,
		`it's "mixed" 'quotes'`,
		`$env:USERNAME`,
		"back`tick",
		`x; Set-Content '` + marker + `' pwned`,
		`$(Set-Content '` + marker + `' pwned)`,
		`x"; Set-Content '` + marker + `' pwned; "`,
	}
	run := func(t *testing.T, pad string) {
		for _, v := range values {
			command := pad + "& " + shellEscape(exe) + " " + shellEscape("-test.run=^TestLegacyArgsHelperProcess$") + " " + legacyArgsEchoMarker + " {{input.x}}"
			resolved := resolveCommandTemplateWith(e.commandValueRenderer(), command, []*ir.Ref{ref}, map[string]any{"x": v}, nil, nil, "", nil)
			out, err := e.toolNodeCommand(context.Background(), resolved, nil).cmd.Output()
			if err != nil {
				t.Fatalf("value %q: %v\ncommand: %s", v, err, resolved)
			}
			var got []string
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); err != nil {
				t.Fatalf("value %q: unreadable echo %q: %v", v, out, err)
			}
			if !reflect.DeepEqual(got, []string{v}) {
				t.Errorf("native program received %q, want exactly [%q]", got, v)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("value %q was executed: %s exists", v, marker)
			}
		}
	}
	t.Run("inline", func(t *testing.T) { run(t, "") })
	// Above psInlineLimit the script is streamed on stdin instead.
	t.Run("streamed", func(t *testing.T) { run(t, "# "+strings.Repeat("x", 12500)+"\n") })
}
