package dispatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startedRoutingManager boots a manager over the seeded fixture (auto-start on)
// and returns it with the store dir and the path of a second tool-only workflow.
func startedRoutingManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "")
	dir := seedManagerFixture(t)
	flow2 := filepath.Join(dir, "flow2.bot")
	body := "tool noop:\n  command: \"true\"\n\nworkflow second:\n  entry: noop\n  noop -> done\n"
	if err := os.WriteFile(flow2, []byte(body), 0o644); err != nil {
		t.Fatalf("write second workflow: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	if m.Current() == nil {
		t.Fatal("the dispatcher did not auto-start")
	}
	return m, dir, flow2
}

// Saving a config that changes assignee_workflows rebuilds the routing of the
// RUNNING dispatcher: no Stop/Start is needed for the new route to exist.
func TestSaveConfigRebuildsRoutingOfRunningDispatcher(t *testing.T) {
	m, _, flow2 := startedRoutingManager(t)

	next, err := m.RawConfig().clone()
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	next.AssigneeWorkflows = map[string]string{"x": flow2}
	if err := m.SaveConfig(next); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rr, ok := m.Current().currentRunner().(*RoutingRunner)
		if ok && rr.ByAssignee["x"] != nil && rr.HasRoute("x") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the running dispatcher still uses the routing built at start (runner %T)", m.Current().currentRunner())
		}
		time.Sleep(10 * time.Millisecond)
	}

	m.Stop()
	m.Stop() // idempotent
	m.mu.Lock()
	retired := m.retired
	m.mu.Unlock()
	if retired != nil {
		t.Fatalf("teardown must close and drop the retired runners, got %d", len(retired))
	}
}

// A workflow that does not compile rejects the save: the file and the running
// config stay as they were and the dispatcher keeps its runner.
func TestSaveConfigRejectsUncompilableRoutingKeepsConfig(t *testing.T) {
	m, dir, _ := startedRoutingManager(t)
	cfgPath := filepath.Join(dir, "dispatcher", "dispatcher.json")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	wfBefore := m.Config().Workflow
	runnerBefore := m.Current().currentRunner()

	next, err := m.RawConfig().clone()
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	next.Workflow = filepath.Join(dir, "missing.bot")
	if err := m.SaveConfig(next); err == nil {
		t.Fatal("SaveConfig accepted a workflow that does not exist")
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("dispatcher.json changed by a rejected save:\n%s", after)
	}
	if got := m.Config().Workflow; got != wfBefore {
		t.Fatalf("Config().Workflow = %q, want %q", got, wfBefore)
	}
	if m.Current().currentRunner() != runnerBefore {
		t.Fatal("the runner was replaced by a rejected save")
	}
}

// A save that leaves workflow / assignee_workflows / bots.paths alone does not
// rebuild (and so does not retire) the runner.
func TestSaveConfigKeepsRunnerWhenRoutingUnchanged(t *testing.T) {
	m, _, _ := startedRoutingManager(t)
	runnerBefore := m.Current().currentRunner()

	next, err := m.RawConfig().clone()
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	next.Polling.IntervalMS = 12345
	if err := m.SaveConfig(next); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if m.Current().currentRunner() != runnerBefore {
		t.Fatal("the runner was rebuilt although the routing did not change")
	}
	m.mu.Lock()
	retired := len(m.retired)
	m.mu.Unlock()
	if retired != 0 {
		t.Fatalf("retired = %d, want 0", retired)
	}
}
