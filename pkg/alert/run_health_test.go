package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/store"
)

// TestStoreSink_StallEpisodeBothEdges: the persisted-twin callback gets
// the stall AND the recovered alert, once each.
func TestStoreSink_StallEpisodeBothEdges(t *testing.T) {
	var persisted []Alert
	base := time.Now()
	clock := base
	m := NewManager(
		WithStallTimeout(5*time.Minute),
		WithStoreSink(func(a Alert) { persisted = append(persisted, a) }),
	)
	m.now = func() time.Time { return clock }

	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "agent", Timestamp: base})

	clock = base.Add(6 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 1 {
		t.Fatalf("want stall, got %+v", fired)
	}
	if len(persisted) != 1 || persisted[0].Kind != KindStall {
		t.Fatalf("persisted = %+v, want one stall", persisted)
	}

	// Activity resumes → the recovered edge closes the episode.
	m.Observe(store.Event{RunID: "r1", Type: store.EventToolCalled, NodeID: "agent", Timestamp: base.Add(7 * time.Minute)})
	if len(persisted) != 2 || persisted[1].Kind != KindStallRecovered {
		t.Fatalf("persisted = %+v, want stall then stall_recovered", persisted)
	}

	// No spurious recovered on further activity (episode already closed).
	m.Observe(store.Event{RunID: "r1", Type: store.EventToolCalled, NodeID: "agent", Timestamp: base.Add(8 * time.Minute)})
	if len(persisted) != 2 {
		t.Fatalf("persisted grew on plain activity: %+v", persisted)
	}
}

// TestObserve_RunHealthReplayIsInert: replaying the persisted twin
// through Observe (the file tail WILL do this) advances nothing — no
// heartbeat, no episode reset, no new alerts.
func TestObserve_RunHealthReplayIsInert(t *testing.T) {
	var persisted []Alert
	base := time.Now()
	clock := base
	m := NewManager(
		WithStallTimeout(5*time.Minute),
		WithStoreSink(func(a Alert) { persisted = append(persisted, a) }),
	)
	m.now = func() time.Time { return clock }

	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "agent", Timestamp: base})
	clock = base.Add(6 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 1 {
		t.Fatalf("want stall, got %+v", fired)
	}

	// The persisted run_health event comes back through the tail: it
	// must NOT re-arm the episode (else the next sweep would re-fire) —
	// and it must not emit a phantom "recovered".
	m.Observe(store.Event{RunID: "r1", Type: store.EventRunHealth, NodeID: "agent", Timestamp: clock})
	if len(persisted) != 1 {
		t.Fatalf("run_health replay produced alerts: %+v", persisted)
	}
	clock = clock.Add(6 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 0 {
		t.Fatalf("run_health replay re-armed the stall episode: %+v", fired)
	}
}

// TestObserve_WorkspaceCheckpointIsNotProgress: the runner's own timer is
// not the run working. A run stuck on a frozen key or a hung tool keeps a
// dirty workspace and keeps being checkpointed — and a failing push repeats
// its event forever — so counting either as progress would let a safety net
// blind the alarm it was laid beside.
func TestObserve_WorkspaceCheckpointIsNotProgress(t *testing.T) {
	base := time.Now()
	clock := base
	m := NewManager(WithStallTimeout(5 * time.Minute))
	m.now = func() time.Time { return clock }

	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "agent", Timestamp: base})
	// Two ticks of the checkpoint loop while the run makes no progress.
	clock = base.Add(2 * time.Minute)
	m.Observe(store.Event{RunID: "r1", Type: store.EventRunWorkspaceCheckpoint, Timestamp: clock})
	clock = base.Add(4 * time.Minute)
	m.Observe(store.Event{RunID: "r1", Type: store.EventRunWorkspaceCheckpoint, Timestamp: clock})

	clock = base.Add(6 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 1 {
		t.Fatalf("the checkpoint ticks kept a stalled run reading as alive: %+v", fired)
	}
	// And a REAL event still re-arms: the exclusion is about the timer,
	// not about silencing the run.
	clock = base.Add(7 * time.Minute)
	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "agent", Timestamp: clock})
	clock = base.Add(8 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 0 {
		t.Fatalf("a real event did not re-arm the run: %+v", fired)
	}
}

// TestObserve_ToolHeartbeatIsProgressAndNamesTheCommand: a shell/script tool
// node vouches for its quiet command with one tool_heartbeat a minute, and
// that is progress — unlike the checkpoint timer, the node emits it itself
// and only while its process lives. When the beats stop (its ceiling), the
// stall text names the silent command, and the next ordinary event closes
// the episode and drops the detail.
func TestObserve_ToolHeartbeatIsProgressAndNamesTheCommand(t *testing.T) {
	base := time.Now()
	clock := base
	m := NewManager(WithStallTimeout(5 * time.Minute))
	m.now = func() time.Time { return clock }

	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "gate", Timestamp: base})
	for i := 1; i <= 9; i++ {
		m.Observe(store.Event{RunID: "r1", Type: store.EventToolHeartbeat, NodeID: "gate",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Data: map[string]any{
				"tool": "shell:gate", "command": "go test ./pkg/...",
				"elapsed_ms": int64(i) * 60000,
			}})
	}
	if fired := m.checkStalls(base.Add(10 * time.Minute)); len(fired) != 0 {
		t.Fatalf("a node whose heartbeat is alive was reported stalled: %+v", fired)
	}

	fired := m.checkStalls(base.Add(16 * time.Minute))
	if len(fired) != 1 || fired[0].Kind != KindStall {
		t.Fatalf("want one stall once the beats stop, got %+v", fired)
	}
	if fired[0].NodeID != "gate" {
		t.Fatalf("stall NodeID = %q, want gate", fired[0].NodeID)
	}
	for _, want := range []string{"no activity for", "shell:gate", "go test ./pkg/..."} {
		if !strings.Contains(fired[0].Reason, want) {
			t.Fatalf("stall reason %q lacks %q", fired[0].Reason, want)
		}
	}

	// A following ordinary event closes the episode and clears the detail.
	m.Observe(store.Event{RunID: "r1", Type: store.EventToolCalled, NodeID: "gate", Timestamp: base.Add(17 * time.Minute)})
	fired = m.checkStalls(base.Add(23 * time.Minute))
	if len(fired) != 1 || fired[0].Kind != KindStall {
		t.Fatalf("want a second stall, got %+v", fired)
	}
	if strings.Contains(fired[0].Reason, "last signal") {
		t.Fatalf("the second stall still names the heartbeat: %q", fired[0].Reason)
	}
}

// TestStoreSink_PanicContained: a panicking store sink must never take
// down the dispatch path.
func TestStoreSink_PanicContained(t *testing.T) {
	base := time.Now()
	clock := base
	sink := &captureSink{}
	m := NewManager(
		WithStallTimeout(5*time.Minute),
		WithSinks(sink),
		WithStoreSink(func(Alert) { panic("boom") }),
	)
	m.now = func() time.Time { return clock }

	m.Observe(store.Event{RunID: "r1", Type: store.EventNodeStarted, NodeID: "agent", Timestamp: base})
	clock = base.Add(6 * time.Minute)
	if fired := m.checkStalls(clock); len(fired) != 1 {
		t.Fatalf("stall lost to a store-sink panic: %+v", fired)
	}
}
