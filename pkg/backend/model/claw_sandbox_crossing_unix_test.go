//go:build unix

package model

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"

	"github.com/arketov/iterion/pkg/backend/delegate"
)

// The stdout os.Pipe failure is the one error path that can strand the stdin
// pair — and it fires exactly when descriptor numbers are scarce, the moment
// a stranded pair costs the most. The cap is placed by retries until the
// shape lands: descriptors this test binary still holds un-finalized from
// earlier tests can close under the finalizer goroutine at any moment — its
// queue drains whether or not the collector is off — so a single placement
// reads as a guess; the retry re-counts a drained, collector-off table and
// places the cap two numbers past its top, where the dispatch's stdin pair
// takes the last two slots and the stdout pipe finds none. The dispatch must
// then fail at the stdout pipe AND leave the count where it was — read with
// the collector still off, so no cycle here can finalize the very pair the
// check hunts — and only a positive delta convicts: a strand can only add
// descriptors, a foreign close shrinks the table, and the placement is
// redone, not believed.
func TestSandboxDispatch_TheStdoutPipeFailureReleasesTheStdinPair(t *testing.T) {
	dir := t.TempDir()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Skipf("no RLIMIT_NOFILE: %v", err)
	}
	saved := lim.Cur
	t.Cleanup(func() {
		lim.Cur = saved
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
	})
	b := newTestClawBackend()
	task := delegate.Task{
		NodeID: "n", Iteration: 1, Model: "openai/gpt-5", WorkDir: dir,
		Sandbox: &scriptedSandbox{script: awaitTaskEnvelope + "exit 0"},
	}
	for attempt := 0; ; attempt++ {
		// Drain the finalizable garbage earlier tests left, then freeze the
		// table: a count or a placement over a table the collector is still
		// shrinking sits on moving ground.
		runtime.GC()
		runtime.GC()
		gcOff := debug.SetGCPercent(-1)
		top := countSelfFDs(t)
		placed := false
		if lim.Max >= uint64(top+2) {
			lim.Cur = uint64(top + 2)
			placed = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim) == nil
		}
		if !placed {
			debug.SetGCPercent(gcOff)
			t.Skipf("cannot place the descriptor cap at %d (hard cap %d)", top+2, lim.Max)
		}
		_, err := b.executeViaSandboxRunner(context.Background(), task)
		lim.Cur = saved
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
		if err == nil || !strings.Contains(err.Error(), "stdout pipe") {
			debug.SetGCPercent(gcOff)
			if attempt >= 4 {
				t.Fatalf("the dispatch did not fail at the stdout pipe in 5 placements (last: %v)", err)
			}
			continue
		}
		// The strand can only ADD descriptors, so only a positive delta
		// convicts, and the count is read with the collector still off: a
		// GC cycle here could finalize the very pair the check hunts. A
		// negative delta is a foreign close — the finalizer goroutine
		// drains its queue of descriptors an earlier test left
		// un-finalized even with the collector off — and the placement is
		// redone, not believed.
		got := countSelfFDs(t) - top
		debug.SetGCPercent(gcOff)
		if got > 0 {
			t.Fatalf("the stdout-pipe failure stranded %d fds — the stdin pair it left behind was not released", got)
		}
		if got < 0 {
			if attempt >= 4 {
				t.Fatalf("the descriptor table kept drifting across 5 placements (last delta: %d)", got)
			}
			continue
		}
		return
	}
}
