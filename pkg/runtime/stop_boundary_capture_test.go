package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dsl/ir"
	"github.com/arketov/iterion/pkg/workspacetrack"
)

// blockingCaptureTracker's CaptureContext blocks until its context is done,
// like a walk over a huge folder, and records what context it was handed.
type blockingCaptureTracker struct {
	workspacetrack.Tracker
	entered     chan struct{}
	ctxLive     bool
	hasDeadline bool
}

func (b *blockingCaptureTracker) CaptureContext(ctx context.Context, _, _, _ string) (*workspacetrack.Snapshot, error) {
	b.ctxLive = ctx.Err() == nil
	_, b.hasDeadline = ctx.Deadline()
	close(b.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// The fail:/pause: boundary is taken when a run stops, i.e. usually with the
// run's context ALREADY cancelled, and the durable status write waits behind
// it. It must run under a live, time-bounded context of its own, so a huge
// workspace can delay a cancel by the budget at most, never hold it for the
// whole scan.
func TestStopBoundaryCaptureCannotHoldTheCancel(t *testing.T) {
	old := stopBoundaryCaptureBudget
	stopBoundaryCaptureBudget = 150 * time.Millisecond
	t.Cleanup(func() { stopBoundaryCaptureBudget = old })

	tracker := &blockingCaptureTracker{entered: make(chan struct{})}
	e := &Engine{workflow: &ir.Workflow{}, workDir: t.TempDir(), workspaceTracker: tracker}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs := &runState{ctx: ctx, runID: "r"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.captureStopBoundary(rs, "n", workspacetrack.PhaseFail)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("captureStopBoundary did not return: the stop-boundary capture holds the cancel")
	}
	select {
	case <-tracker.entered:
	default:
		t.Fatal("scenario broken: the tracker was never consulted")
	}
	if !tracker.ctxLive {
		t.Error("the capture was handed the already-cancelled run context, so it would abort before taking the boundary")
	}
	if !tracker.hasDeadline {
		t.Error("the capture context has no deadline, so a huge workspace would hold the cancel")
	}
}
