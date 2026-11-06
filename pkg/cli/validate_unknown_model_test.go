package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/backend/modelspecs"
	"github.com/arketov/iterion/pkg/cli"
)

func validateWithModel(t *testing.T, model string) cli.ValidateResult {
	t.Helper()
	t.Cleanup(modelspecs.SetDefault(modelspecs.NewSeeded(nil)))
	src := strings.Replace(minimalWorkflow, `"test-model"`, `"`+model+`"`, 1)
	path := writeFixture(t, t.TempDir(), "m.bot", src)

	p, buf := newTestPrinter(cli.OutputJSON)
	if err := cli.RunValidate(path, p); err != nil {
		t.Fatalf("RunValidate: %v", err)
	}
	var result cli.ValidateResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("cannot parse JSON output: %v", err)
	}
	return result
}

func countC338(r cli.ValidateResult) (n int) {
	for _, d := range r.Diagnostics {
		if d.Code == "C338" {
			n++
		}
	}
	return n
}

func TestValidate_UnknownModelWarnsC338(t *testing.T) {
	r := validateWithModel(t, "anthropic/claude-sonnet-9-9")
	if !r.Valid {
		t.Errorf("a warning must not invalidate the workflow: %+v", r)
	}
	if n := countC338(r); n != 1 {
		t.Fatalf("C338 count = %d, want 1: %+v", n, r.Diagnostics)
	}
	for _, d := range r.Diagnostics {
		if d.Code == "C338" && (d.Severity != "warning" || d.NodeID != "reviewer" || d.Hint == "") {
			t.Errorf("bad C338: %+v", d)
		}
	}
	if len(r.CompileDiagnostics) != 0 {
		t.Errorf("C338 must stay out of compile_diagnostics: %v", r.CompileDiagnostics)
	}
}

func TestValidate_KnownModelDoesNotWarnC338(t *testing.T) {
	r := validateWithModel(t, "anthropic/claude-sonnet-5-5")
	if n := countC338(r); n != 0 {
		t.Errorf("a registry model warned: %+v", r.Diagnostics)
	}
}
