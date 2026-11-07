package plugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ITERION_HOME", home)
	writeFileT(t, filepath.Join(home, pluginsSubdir, "lc-demo", ManifestFile),
		"name: lc-demo\ncontributes:\n  lifecycle:\n    index: echo \"indexing {{workspace}}\" && echo ok > lc-marker.txt\n")
	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	workspace := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := RunLifecycle(context.Background(), reg, "lc-demo", "index", workspace, &stdout, &stderr); err != nil {
		t.Fatalf("RunLifecycle: %v (stderr: %s)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "indexing "+workspace) {
		t.Errorf("stdout missing expanded {{workspace}}: %q", out)
	}
	// A relative write proves the command ran with Dir = workspace, whatever
	// the host shell prints for a working directory (pwd is a POSIX path
	// under Git Bash on Windows).
	if _, err := os.Stat(filepath.Join(workspace, "lc-marker.txt")); err != nil {
		t.Errorf("the command did not run with Dir = workspace: %v", err)
	}

	// Error paths are explicit.
	if err := RunLifecycle(context.Background(), reg, "lc-demo", "compact", workspace, &stdout, &stderr); err == nil {
		t.Error("unknown phase accepted")
	}
	if err := RunLifecycle(context.Background(), reg, "lc-demo", "refresh", workspace, &stdout, &stderr); err == nil {
		t.Error("empty refresh command accepted")
	}
	if err := RunLifecycle(context.Background(), reg, "nope", "index", workspace, &stdout, &stderr); err == nil {
		t.Error("unknown plugin accepted")
	}
}
