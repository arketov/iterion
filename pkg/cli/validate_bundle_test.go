package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/cli"
)

// writeBundle lays down a manifest.yaml + main.bot bundle in dir.
func writeBundle(t *testing.T, dir, manifest, mainBot string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.bot"), []byte(mainBot), 0o644); err != nil {
		t.Fatal(err)
	}
}

type validateJSON struct {
	Valid             bool     `json:"valid"`
	BundleDiagnostics []string `json:"bundle_diagnostics"`
}

func runValidateJSON(t *testing.T, dir string) (validateJSON, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	p := &cli.Printer{W: buf, Format: cli.OutputJSON}
	err := cli.RunValidate(dir, p)
	var res validateJSON
	if jsonErr := json.Unmarshal(buf.Bytes(), &res); jsonErr != nil {
		t.Fatalf("unmarshal validate JSON: %v\nraw: %s", jsonErr, buf.String())
	}
	return res, err
}

func hasCode(diags []string, code string) bool {
	for _, d := range diags {
		if strings.Contains(d, code) {
			return true
		}
	}
	return false
}

// TestRunValidate_BundleVarTypoWarns proves the bundlelint wiring: a
// dispatch_vars key that names no workflow var surfaces as a C200 bundle
// diagnostic, but stays a warning (validation still passes).
func TestRunValidate_BundleVarTypoWarns(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir,
		`name: testbot
dispatch_vars:
  reel_var: "{{issue.title}}"
`,
		`vars:
  real_var: string = "x"

prompt sys:
  hello {{vars.real_var}}

agent only:
  backend: "claw"
  model: "anthropic/claude-sonnet-4-6"
  system: sys

workflow w:
  entry: only
  only -> done
`)

	res, err := runValidateJSON(t, dir)
	if err != nil {
		t.Fatalf("expected validation to pass (warning only), got error: %v", err)
	}
	if !res.Valid {
		t.Error("expected valid=true for a warning-only bundle")
	}
	if !hasCode(res.BundleDiagnostics, "C200") {
		t.Errorf("expected a C200 bundle diagnostic, got %v", res.BundleDiagnostics)
	}
}

// TestRunValidate_PerBotMemoryNameMismatchFails proves an error-severity
// bundle diagnostic (C230) flips validity and makes RunValidate return an
// error — the per-bot-memory name-stability invariant enforced pre-run.
func TestRunValidate_PerBotMemoryNameMismatchFails(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir,
		`name: declaredbot
`,
		`prompt sys:
  hello

agent only:
  backend: "claw"
  model: "anthropic/claude-sonnet-4-6"
  system: sys
  memory:
    enabled: true
    visibility: "bot"
    scope: "x"

workflow mismatchwf:
  entry: only
  only -> done
`)

	res, err := runValidateJSON(t, dir)
	if err == nil {
		t.Error("expected RunValidate to return an error for a C230 (error) bundle diagnostic")
	}
	if res.Valid {
		t.Error("expected valid=false when an error-severity bundle diagnostic fires")
	}
	if !hasCode(res.BundleDiagnostics, "C230") {
		t.Errorf("expected a C230 bundle diagnostic, got %v", res.BundleDiagnostics)
	}
}
