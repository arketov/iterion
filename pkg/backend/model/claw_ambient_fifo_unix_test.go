//go:build unix

package model

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/arketov/iterion/pkg/backend/ambient"
)

// A CLAUDE.md the walk would never load shadows nothing: the shadow exists
// to say where the walk has spoken, and a non-regular file — a FIFO here, a
// socket or a device the same way — is refused by the walk's own readWithin.
func TestClawAmbientBlockShadowsOnlyWhatTheWalkWouldLoad(t *testing.T) {
	_, _, repo, work := clawTree(t)
	clawWrite(t, filepath.Join(repo, "AGENTS.md"), "MARKER-FIFO-AGENTS")
	// clawTree plants a CLAUDE.md for its own default tree; the FIFO takes
	// its exact place.
	if err := os.Remove(filepath.Join(repo, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(repo, "CLAUDE.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	block := clawAmbientBlockOf(t, work, ambient.Workspace)
	if !strings.Contains(block, "MARKER-FIFO-AGENTS") {
		t.Errorf("workspace: a non-regular CLAUDE.md the walk refuses still shadowed the repo's AGENTS.md:\n%s", block)
	}
}
