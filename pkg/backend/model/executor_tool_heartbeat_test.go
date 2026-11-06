package model

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/backend/tool"
	"github.com/arketov/iterion/pkg/dsl/ir"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

// A shell/script tool node prints nothing to the event stream while its
// command runs, so a long quiet command (a `go test` over big packages) read
// as silence to both stall detectors and a live run was taken down. The node
// now vouches for its command with tool_heartbeat events until it finishes.
//
// Seen red: before the fix the hook and the event do not exist (the test does
// not compile); with the beat started but not stopped before the finish event,
// the order assertion and the "no beat after return" arm fail.
func TestToolNodeHeartbeatsWhileItsCommandIsSilent(t *testing.T) {
	command := "sleep 0.4"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 400"
	}

	var mu sync.Mutex
	var seq []string
	var beats []ToolNodeHeartbeatInfo
	var beatNodes []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		seq = append(seq, s)
	}
	ex := newTestClawExecutor(NewRegistry(), &ir.Workflow{},
		WithToolPolicy(tool.OpenPolicy()),
		WithEventHooks(EventHooks{
			OnToolStarted: func(string, LLMToolStartedInfo) { record("started") },
			OnToolNodeHeartbeat: func(nodeID string, info ToolNodeHeartbeatInfo) {
				mu.Lock()
				defer mu.Unlock()
				seq = append(seq, "beat")
				beats = append(beats, info)
				beatNodes = append(beatNodes, nodeID)
			},
			OnToolCall: func(string, LLMToolCallInfo) { record("called") },
		}))
	ex.toolHeartbeatEvery = 20 * time.Millisecond

	node := &ir.ToolNode{BaseNode: ir.BaseNode{ID: "gate"}, Command: command}
	if _, err := ex.Execute(context.Background(), node, map[string]any{}); err != nil {
		t.Fatalf("the node failed: %v", err)
	}

	mu.Lock()
	n := len(beats)
	got := append([]ToolNodeHeartbeatInfo(nil), beats...)
	nodes := append([]string(nil), beatNodes...)
	order := append([]string(nil), seq...)
	mu.Unlock()

	if n < 2 {
		t.Fatalf("got %d heartbeats during a 400ms silent command at a 20ms interval, want at least 2", n)
	}
	for i, b := range got {
		if nodes[i] != "gate" || b.ToolName != "shell:gate" {
			t.Fatalf("beat %d = node %q %+v, want node gate, tool shell:gate", i, nodes[i], b)
		}
		if !strings.Contains(b.Command, command) {
			t.Fatalf("beat %d command %q does not carry the node's command %q", i, b.Command, command)
		}
		if i > 0 && b.Elapsed <= got[i-1].Elapsed {
			t.Fatalf("elapsed does not grow: %v then %v", got[i-1].Elapsed, b.Elapsed)
		}
	}
	if order[0] != "started" || order[len(order)-1] != "called" {
		t.Fatalf("event order = %v, want started first and called last", order)
	}

	// stop waited for the beat goroutine: nothing beats once Execute is back.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	after := len(beats)
	mu.Unlock()
	if after != n {
		t.Fatalf("%d heartbeats after the node returned, want none", after-n)
	}
}

func TestToolNodeHeartbeatStopsAtItsCeiling(t *testing.T) {
	var logged lockedSink
	var mu sync.Mutex
	count := 0
	ex := newTestClawExecutor(NewRegistry(), &ir.Workflow{},
		WithLogger(iterlog.New(iterlog.LevelInfo, &logged)),
		WithEventHooks(EventHooks{
			OnToolNodeHeartbeat: func(string, ToolNodeHeartbeatInfo) {
				mu.Lock()
				count++
				mu.Unlock()
			},
		}))
	read := func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}

	stop := ex.startToolNodeHeartbeat("gate", "shell:gate", "go test ./...", 10*time.Millisecond, 60*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	atCeiling := read()
	if atCeiling < 1 || atCeiling > 7 {
		t.Fatalf("got %d heartbeats under a 60ms ceiling at 10ms, want between 1 and max/every+1", atCeiling)
	}
	time.Sleep(60 * time.Millisecond)
	if read() != atCeiling {
		t.Fatalf("heartbeats kept coming past the ceiling: %d then %d", atCeiling, read())
	}
	if n := strings.Count(logged.String(), toolNodeHeartbeatMaxEnv); n != 1 {
		t.Fatalf("the log names %s %d times, want exactly one warning: %q", toolNodeHeartbeatMaxEnv, n, logged.String())
	}
	stop()
	stop() // idempotent

	// No ceiling: the beats run until stop.
	mu.Lock()
	count = 0
	mu.Unlock()
	stop = ex.startToolNodeHeartbeat("gate", "shell:gate", "go test ./...", 10*time.Millisecond, 0)
	time.Sleep(150 * time.Millisecond)
	running := read()
	if running < 5 {
		t.Fatalf("got %d heartbeats without a ceiling in 150ms at 10ms, want at least 5", running)
	}
	stop()
	final := read()
	time.Sleep(50 * time.Millisecond)
	if read() != final {
		t.Fatalf("a heartbeat fired after stop returned")
	}
}

func TestResolveToolNodeHeartbeatMax(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		want     time.Duration
		wantWarn bool
	}{
		{"", defaultToolNodeHeartbeatMax, false},
		{"10m", 10 * time.Minute, false},
		{"0", 0, true},
		{"off", 0, true},
		{"UNBOUNDED", 0, true},
		{"banana", defaultToolNodeHeartbeatMax, true},
		{"-5s", defaultToolNodeHeartbeatMax, true},
	} {
		var once sync.Once
		var warn bytes.Buffer
		if got := resolveToolNodeHeartbeatMax(tc.raw, &once, &warn); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.raw, got, tc.want)
		}
		// A second resolution must not repeat the warning.
		resolveToolNodeHeartbeatMax(tc.raw, &once, &warn)
		n := strings.Count(warn.String(), toolNodeHeartbeatMaxEnv)
		if tc.wantWarn && n != 1 || !tc.wantWarn && n != 0 {
			t.Errorf("%q: warning named %s %d times (%q), wantWarn=%v", tc.raw, toolNodeHeartbeatMaxEnv, n, warn.String(), tc.wantWarn)
		}
	}
}

func TestToolNodeHeartbeatHookPersistsEvent(t *testing.T) {
	t.Parallel()
	em := &captureEmitter{}
	hooks := NewStoreEventHooks(context.Background(), em, "run-hb", iterlog.Nop(), nil, nil)
	if hooks.OnToolNodeHeartbeat == nil {
		t.Fatal("the store hooks do not register OnToolNodeHeartbeat")
	}
	hooks.OnToolNodeHeartbeat("gate", ToolNodeHeartbeatInfo{ToolName: "shell:gate", Command: "go test ./...", Elapsed: 61500 * time.Millisecond})
	var found []store.Event
	for _, e := range em.events {
		if e.Type == store.EventToolHeartbeat {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d tool_heartbeat events among %d events, want 1", len(found), len(em.events))
	}
	e := found[0]
	if e.NodeID != "gate" || e.Data["tool"] != "shell:gate" || e.Data["command"] != "go test ./..." || e.Data["elapsed_ms"] != int64(61500) {
		t.Fatalf("event = %+v", e)
	}
}
