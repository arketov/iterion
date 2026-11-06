package native

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iterlog "github.com/arketov/iterion/pkg/log"
)

// The periodic verification asks for a rebuild every ten seconds. With
// issues/ gone every pass fails the same way: that is ONE line in the log,
// not one per tick, and a recovery followed by a second disappearance is
// reported again.
func TestRebuildAsync_LogsAFailureOncePerDistinctError(t *testing.T) {
	refuseWatch(t)
	setRescanInterval(t, 0)
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var buf bytes.Buffer
	s.SetLogger(iterlog.NewFallback(iterlog.LevelWarn, &buf))

	count := func() int { return strings.Count(buf.String(), "index rebuild failed") }
	pass := func(what string) {
		s.rebuildAsync(what)
		waitRebuildIdle(t, s)
	}

	issues := filepath.Join(dir, issuesDir)
	if err := os.RemoveAll(issues); err != nil {
		t.Fatal(err)
	}
	pass("periodic verification")
	pass("periodic verification")
	pass("kernel event queue overflow")
	pass("periodic verification")
	if got := count(); got != 1 {
		t.Fatalf("four failing rebuilds over the same missing issues/ produced %d error lines, want 1\n%s", got, buf.String())
	}

	if err := os.MkdirAll(issues, dirPerm); err != nil {
		t.Fatal(err)
	}
	pass("periodic verification")
	if !strings.Contains(buf.String(), "index rebuild succeeds again") {
		t.Fatalf("recovery was not reported\n%s", buf.String())
	}

	if err := os.RemoveAll(issues); err != nil {
		t.Fatal(err)
	}
	pass("periodic verification")
	pass("periodic verification")
	if got := count(); got != 2 {
		t.Fatalf("a second disappearance after a recovery produced %d error lines in total, want 2\n%s", got, buf.String())
	}
}
