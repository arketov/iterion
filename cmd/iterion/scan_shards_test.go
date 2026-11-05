package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/store"
)

// TestPlanShards_DeterministicIDs exercises planShards' contract:
// given the same parent run id, file list, and shard size, the plan
// must be byte-identical across calls (deterministic run ids let
// `iterion __scan-shards` survive interruptions: a re-invocation
// reattaches to the previously-spawned child runs instead of
// orphaning them and creating a fresh set).
func TestPlanShards_DeterministicIDs(t *testing.T) {
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	parent := "parent-abc"
	p1 := planShards(files, 2, parent)
	p2 := planShards(files, 2, parent)
	if len(p1) != 3 {
		t.Fatalf("expected 3 shards for 5 files at size 2, got %d", len(p1))
	}
	for i := range p1 {
		if p1[i].RunID != p2[i].RunID {
			t.Errorf("shard %d id drift: %s vs %s", i, p1[i].RunID, p2[i].RunID)
		}
		if p1[i].Index != p2[i].Index {
			t.Errorf("shard %d index drift: %d vs %d", i, p1[i].Index, p2[i].Index)
		}
	}
}

// Different parent ids must produce disjoint run id namespaces so two
// independent parent runs don't collide on the same child id.
func TestPlanShards_DifferentParentsDisjoint(t *testing.T) {
	files := []string{"a.go", "b.go", "c.go"}
	a := planShards(files, 1, "parent-a")
	b := planShards(files, 1, "parent-b")
	seen := map[string]bool{}
	for _, s := range a {
		seen[s.RunID] = true
	}
	for _, s := range b {
		if seen[s.RunID] {
			t.Errorf("run id %s collision between parent-a and parent-b", s.RunID)
		}
	}
}

// Two scans with the SAME parent and SAME file COUNT but DIFFERENT files
// must get disjoint run ids. The prior seed keyed only on len(files), so
// equal-length/different-content lists collided and the no-clobber store
// rejected the second scan's shards with "already exists" (deepsec
// HIGH_BUG, 2026-06-22 dogfood). A different shard_size must likewise
// repartition into disjoint ids.
func TestPlanShards_DifferentFileListsDisjoint(t *testing.T) {
	parent := "p"
	a := planShards([]string{"a.go", "b.go", "c.go"}, 2, parent)
	b := planShards([]string{"x.go", "y.go", "z.go"}, 2, parent) // same count, different files
	seen := map[string]bool{}
	for _, s := range a {
		seen[s.RunID] = true
	}
	for _, s := range b {
		if seen[s.RunID] {
			t.Errorf("run id %s collides across different file lists of equal length", s.RunID)
		}
	}
	// Same files, different shard_size ⇒ disjoint ids too.
	for _, s := range planShards([]string{"a.go", "b.go", "c.go"}, 3, parent) {
		if seen[s.RunID] {
			t.Errorf("run id %s collides across different shard sizes", s.RunID)
		}
	}
}

// Last shard correctly carries the remainder when len(files) is not a
// multiple of shard_size. A bug here would silently drop files.
func TestPlanShards_LastShardRemainder(t *testing.T) {
	files := []string{"a", "b", "c", "d", "e", "f", "g"}
	plan := planShards(files, 3, "p")
	if len(plan) != 3 {
		t.Fatalf("expected 3 shards (3+3+1), got %d", len(plan))
	}
	wantLens := []int{3, 3, 1}
	for i, w := range wantLens {
		if len(plan[i].Files) != w {
			t.Errorf("shard %d: expected %d files, got %d", i, w, len(plan[i].Files))
		}
	}
	totalFiles := 0
	for _, s := range plan {
		totalFiles += len(s.Files)
	}
	if totalFiles != len(files) {
		t.Errorf("file count drift: planned %d, input %d (files dropped)", totalFiles, len(files))
	}
}

// Run ids must be in the documented format "shard-<8 bytes hex>" so
// they can be filtered / globbed in the run store.
func TestPlanShards_RunIDFormat(t *testing.T) {
	plan := planShards([]string{"a"}, 1, "p")
	if len(plan) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(plan))
	}
	id := plan[0].RunID
	if !strings.HasPrefix(id, "shard-") {
		t.Errorf("run id missing 'shard-' prefix: %s", id)
	}
	if len(id) != len("shard-")+16 { // 8 bytes hex = 16 chars
		t.Errorf("run id wrong length: got %d, want %d (shard- + 16 hex chars)", len(id), len("shard-")+16)
	}
}

// Empty file list produces zero shards. Callers depend on this to
// short-circuit "no files to scan" without an extra branch.
func TestPlanShards_EmptyInput(t *testing.T) {
	if got := planShards(nil, 10, "p"); len(got) != 0 {
		t.Errorf("expected 0 shards for nil input, got %d", len(got))
	}
	if got := planShards([]string{}, 10, "p"); len(got) != 0 {
		t.Errorf("expected 0 shards for empty slice, got %d", len(got))
	}
}

// awaitTerminal MUST poll until every result reaches a terminal
// status — the original "one-shot LoadRun" shape silently marked
// cloud-mode shards as failed because they were still queued when the
// poll fired. The test below seeds two runs at non-terminal statuses,
// then concurrently transitions them to finished/failed to confirm
// the poll loop converges only when the store reflects it.
func TestAwaitTerminal_PollsUntilTerminal(t *testing.T) {
	rs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ctx := context.Background()
	if _, err := rs.CreateRun(ctx, "shard-aaaa00000000aaaa", "wf", nil); err != nil {
		t.Fatalf("CreateRun a: %v", err)
	}
	if _, err := rs.CreateRun(ctx, "shard-bbbb00000000bbbb", "wf", nil); err != nil {
		t.Fatalf("CreateRun b: %v", err)
	}

	results := []shardResult{
		{Plan: shardPlan{Index: 0, RunID: "shard-aaaa00000000aaaa"}},
		{Plan: shardPlan{Index: 1, RunID: "shard-bbbb00000000bbbb"}},
	}

	// Capture the parent ctx into a separate var before the deadline
	// reassign below — the goroutine closes over `bgCtx` instead of `ctx`
	// so the closure's read never races against the `ctx, cancel := ...`
	// write on the main goroutine. (Pre-existing race detected by
	// `task test:race`.)
	bgCtx := ctx
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = rs.UpdateRunStatus(bgCtx, "shard-aaaa00000000aaaa", store.RunStatusFinished, "")
		time.Sleep(30 * time.Millisecond)
		_ = rs.UpdateRunStatus(bgCtx, "shard-bbbb00000000bbbb", store.RunStatusFailed, "boom")
	}()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	awaitTerminal(ctx, rs, results, 10*time.Millisecond)

	if results[0].Status != store.RunStatusFinished {
		t.Errorf("shard 0 status = %q, want finished", results[0].Status)
	}
	if results[1].Status != store.RunStatusFailed {
		t.Errorf("shard 1 status = %q, want failed", results[1].Status)
	}
	if results[1].Error != "boom" {
		t.Errorf("shard 1 error = %q, want %q", results[1].Error, "boom")
	}
}

// When the context cancels before all runs terminate, awaitTerminal
// must annotate the still-pending shards with a timeout error rather
// than block forever or return a partial silent state.
func TestAwaitTerminal_ContextCancelMarksPending(t *testing.T) {
	rs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if _, err := rs.CreateRun(context.Background(), "shard-deadbeefdeadbeef", "wf", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	results := []shardResult{{Plan: shardPlan{Index: 0, RunID: "shard-deadbeefdeadbeef"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	awaitTerminal(ctx, rs, results, 10*time.Millisecond)

	if !strings.Contains(results[0].Error, "timed out") {
		t.Errorf("expected timeout error, got %q", results[0].Error)
	}
}

// A LoadRun miss (run document not yet visible — cloud publisher lag
// is the realistic case) must keep the shard in the polling set
// rather than mark it permanently missing.
func TestAwaitTerminal_LoadRunMissTransient(t *testing.T) {
	rs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	results := []shardResult{{Plan: shardPlan{Index: 0, RunID: "shard-nonexistent00"}}}

	go func() {
		time.Sleep(40 * time.Millisecond)
		_, _ = rs.CreateRun(context.Background(), "shard-nonexistent00", "wf", nil)
		time.Sleep(10 * time.Millisecond)
		_ = rs.UpdateRunStatus(context.Background(), "shard-nonexistent00", store.RunStatusFinished, "")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	awaitTerminal(ctx, rs, results, 10*time.Millisecond)

	if results[0].Status != store.RunStatusFinished {
		t.Errorf("status = %q, want finished (transient miss should not be permanent)", results[0].Status)
	}
}

// A shard that already failed AT or BEFORE dispatch (r.Error set, run
// document never created — cloud-mode pre-launch failures like a bad
// ITERION_SERVER_URL, an unreadable workflow, or a request-build/POST/non-2xx
// error) is already terminal: awaitTerminal must report it immediately, NOT
// keep polling a document that will never appear until ctx timeout. Regression
// for the multi-hour hang Revi (review-pr) caught reviewing the campaign diff.
func TestAwaitTerminal_PreDispatchFailureDoesNotHang(t *testing.T) {
	rs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	// Deliberately NO CreateRun — the run document never exists.
	results := []shardResult{{
		Plan:  shardPlan{Index: 0, RunID: "shard-neverlaunched"},
		Error: "build launch request: net/url: invalid control character in URL",
	}}

	// Long ctx so a regression hangs visibly; the watchdog below asserts
	// awaitTerminal returns fast (first tick) rather than via ctx timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { awaitTerminal(ctx, rs, results, 10*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("awaitTerminal hung on a pre-dispatch failure (no run document) instead of reporting it immediately")
	}

	if results[0].Status != store.RunStatusFailed {
		t.Errorf("status = %q, want failed", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "build launch request") {
		t.Errorf("error = %q, want the original pre-dispatch error preserved (not a timeout)", results[0].Error)
	}
}
