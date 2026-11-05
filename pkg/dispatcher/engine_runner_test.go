package dispatcher

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	iterlog "github.com/arketov/iterion/pkg/log"
)

// TestEngineRunnerAcceptsBundleDir verifies that an unpacked bundle
// directory (manifest.yaml + main.bot alongside) is a valid workflow
// source — the dispatcher can run it just like a plain `.bot`.
func TestEngineRunnerAcceptsBundleDir(t *testing.T) {
	// bots/secured-renovacy/ has the canonical bundle shape:
	// manifest.yaml + main.bot + prompts/. Walk up from the test
	// binary's working dir (pkg/dispatcher) to the repo root.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	bundleDir := filepath.Join(repoRoot, "examples", "secured-renovacy")
	if _, err := os.Stat(filepath.Join(bundleDir, "manifest.yaml")); err != nil {
		t.Skipf("bundle fixture absent at %s: %v", bundleDir, err)
	}

	logger := iterlog.New(iterlog.LevelError, &bytes.Buffer{})
	r, err := NewEngineRunner(bundleDir, logger)
	if err != nil {
		t.Fatalf("NewEngineRunner on bundle dir: %v", err)
	}
	defer r.Close()

	if r.Workflow() == nil {
		t.Fatal("compiled workflow is nil")
	}
	if r.workflowHash == "" {
		t.Fatal("workflow hash is empty")
	}
	// The compiled bundle path should resolve to the bundle's
	// inner .bot, not the directory.
	if !filepath.IsAbs(r.workflowPath) {
		t.Fatalf("workflowPath should be absolute: %s", r.workflowPath)
	}
}

// TestEngineRunnerAcceptsPlainBot confirms the default (non-bundle)
// path still works after the bundle branch was added.
func TestEngineRunnerAcceptsPlainBot(t *testing.T) {
	dir := t.TempDir()
	bot := filepath.Join(dir, "hello.bot")
	src := "workflow hello:\n  entry: done\n"
	if err := os.WriteFile(bot, []byte(src), 0o644); err != nil {
		t.Fatalf("write bot: %v", err)
	}

	logger := iterlog.New(iterlog.LevelError, &bytes.Buffer{})
	r, err := NewEngineRunner(bot, logger)
	if err != nil {
		t.Fatalf("NewEngineRunner: %v", err)
	}
	defer r.Close()
	if r.bundle != nil {
		t.Fatal("plain .bot should not carry a bundle handle")
	}
}
