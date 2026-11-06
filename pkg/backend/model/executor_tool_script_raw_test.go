package model

import (
	"context"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
	"github.com/arketov/iterion/pkg/dsl/parser/parsertest"
)

// A backtick raw string in `script:` carries the two characters `\` `n` of a
// JS literal. The contract is that they reach the interpreter's file as those
// two characters: iterion adds no escape reading of its own (the interpreter
// reads `\n` inside its own literal). These tests pin the contract stage by
// stage — compiled IR, rendered body, temp file, a real node run — so a
// regression names the stage that turned the pair into a line break.

const rawScriptJS = "process.stdout.write(JSON.stringify({n: 'a\\nb'.length}))"

// rawScriptSource is a .bot whose tool node holds rawScriptJS as a raw string.
func rawScriptSource(header string) string {
	return header + "tool t:\n  language: js\n  script: `" + rawScriptJS + "`\n\nworkflow w:\n  entry: t\n  t -> done\n"
}

func compileRawScriptNode(t *testing.T, header string) *ir.ToolNode {
	t.Helper()
	pr := parsertest.Parse("raw.bot", rawScriptSource(header))
	wf := ir.Compile(pr.File).Workflow
	node, ok := wf.Nodes["t"].(*ir.ToolNode)
	if !ok {
		t.Fatalf("node t is not a tool node: %T", wf.Nodes["t"])
	}
	return node
}

func TestScriptRawStringKeepsBackslashN_IR(t *testing.T) {
	for name, header := range map[string]string{
		"profile 1": "",
		"profile 2": "dsl: 2\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			node := compileRawScriptNode(t, header)
			if node.Script != rawScriptJS {
				t.Fatalf("Script = %q, want %q", node.Script, rawScriptJS)
			}
			if !strings.Contains(node.Script, `'a\nb'`) || strings.Contains(node.Script, "a\nb") {
				t.Fatalf("the backslash-n pair did not survive compilation: %q", node.Script)
			}
		})
	}
}

func TestScriptRawStringKeepsBackslashN_Render(t *testing.T) {
	node := compileRawScriptNode(t, "dsl: 2\n\n")
	script := node.Script + "; var c = {{input.msg}}"
	refs, err := ir.ParseRefs(script)
	if err != nil {
		t.Fatal(err)
	}
	body := resolveScriptTemplate(script, refs, map[string]any{"msg": "x\ny"}, nil, nil, "")
	if !strings.Contains(body, rawScriptJS) {
		t.Fatalf("author text changed by rendering: %q", body)
	}
	if !strings.HasSuffix(body, `; var c = "x\ny"`) {
		t.Fatalf("a referenced value must be a JSON literal, got %q", body)
	}
}

func TestScriptRawStringKeepsBackslashN_File(t *testing.T) {
	node := compileRawScriptNode(t, "dsl: 2\n\n")
	e := newTestClawExecutor(NewRegistry(), &ir.Workflow{}, WithWorkDir(t.TempDir()))
	resolve, buildCmd := e.scriptRecipe(context.Background(), node, map[string]any{})
	resolved := resolve()
	tc, cleanup, err := buildCmd(resolved)
	if err != nil {
		t.Fatalf("buildCmd: %v", err)
	}
	defer cleanup()
	got, err := os.ReadFile(tc.cmd.Args[len(tc.cmd.Args)-1])
	if err != nil {
		t.Fatalf("read the script file: %v", err)
	}
	if string(got) != rawScriptJS {
		t.Fatalf("script file = %q, want %q byte for byte", got, rawScriptJS)
	}
}

func TestScriptRawStringKeepsBackslashN_Node(t *testing.T) {
	if _, err := osexec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	node := compileRawScriptNode(t, "dsl: 2\n\n")
	e := newTestClawExecutor(NewRegistry(), &ir.Workflow{}, WithWorkDir(t.TempDir()))
	resolve, buildCmd := e.scriptRecipe(context.Background(), node, map[string]any{})
	tc, cleanup, err := buildCmd(resolve())
	if err != nil {
		t.Fatalf("buildCmd: %v", err)
	}
	defer cleanup()
	// `'a\nb'` is one literal for node: length 3 (a, LF, b). A real line break
	// inside the quotes would be a SyntaxError instead.
	out, err := tc.cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != `{"n":3}` {
		t.Fatalf("node did not run the script as written: %v: %s", err, out)
	}
}
