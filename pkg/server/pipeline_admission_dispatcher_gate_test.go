package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher"
	"github.com/arketov/iterion/pkg/dispatcher/native"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/runview"
	"github.com/arketov/iterion/pkg/store"
)

// admissionGateEnv is a local studio server with a native board, a probe bot
// and a recording launch publisher, for the admission loop's gates.
type admissionGateEnv struct {
	srv   *Server
	board *native.Store
	pub   *tierPublisher
}

func newAdmissionGateEnv(t *testing.T, board *native.Store) *admissionGateEnv {
	t.Helper()
	s := newOrgTestServer(t)
	seedGate(t, s, gateSpec{id: "t1"})
	botsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(botsDir, "probe.bot"), []byte(tierBakedBot), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Bots.Paths = []string{botsDir}
	if board == nil {
		var err error
		board, err = native.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = board.Close() })
	}
	s.cfg.NativeTrackerStore = board
	rs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Store = rs
	pub := &tierPublisher{}
	s.runs = newTestRunviewService(t, "", runview.WithStore(rs), runview.WithLaunchPublisher(pub))
	if !s.pipelineAdmissionEnabled() {
		t.Fatal("precondition: the local admission loop must be enabled here")
	}
	return &admissionGateEnv{srv: s, board: board, pub: pub}
}

func (e *admissionGateEnv) launches() int {
	e.pub.mu.Lock()
	defer e.pub.mu.Unlock()
	return len(e.pub.specs)
}

func (e *admissionGateEnv) readyTicket(t *testing.T) *native.Issue {
	t.Helper()
	iss, err := e.board.Create(native.Issue{Title: "ready ticket", State: native.StateReady, Bot: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

// newConfiguredDispatcher builds a real Manager over board with a saved
// dispatcher config (so Status().HasConfig is true), not yet started.
func newConfiguredDispatcher(t *testing.T, board *native.Store) *dispatcher.Manager {
	t.Helper()
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dispatcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, "flow.bot")
	body := "tool noop:\n  command: \"true\"\n\nworkflow minimal:\n  entry: noop\n  noop -> done\n"
	if err := os.WriteFile(wf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{"workflow":"` + filepath.ToSlash(wf) + `","tracker":{"kind":"native"}}`)
	if err := os.WriteFile(filepath.Join(dir, "dispatcher", "dispatcher.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := dispatcher.NewManager(dispatcher.ManagerOptions{
		StoreDir:    dir,
		NativeStore: board,
		Logger:      iterlog.Nop(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	return m
}

// A saved dispatcher config means the dispatcher owns the board: the studio's
// launch loop must not start Ready cards beside it, whether the dispatcher is
// idle, stopped or paused. Before the fix the loop stood down only while the
// manager was RUNNING, so Stop / Pause handed the board to the studio, which
// launched the very card the operator had just interrupted.
func TestAdmissionLoopStandsDownForAConfiguredDispatcher(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, m *dispatcher.Manager)
	}{
		{"idle with config", func(*testing.T, *dispatcher.Manager) {}},
		{"stopped", func(t *testing.T, m *dispatcher.Manager) {
			if err := m.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			m.Stop()
		}},
		{"paused", func(t *testing.T, m *dispatcher.Manager) {
			if err := m.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := m.Pause(); err != nil {
				t.Fatalf("Pause: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board, err := native.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = board.Close() })
			env := newAdmissionGateEnv(t, board)
			m := newConfiguredDispatcher(t, board)
			tc.prep(t, m)
			if !m.Status().HasConfig {
				t.Fatal("precondition: the manager must report a saved config")
			}
			env.srv.cfg.Dispatcher = m
			// The card turns Ready only now, so a live dispatcher actor cannot
			// have claimed it before the loop looks.
			iss := env.readyTicket(t)

			env.srv.admitReadyPipelines()

			if n := env.launches(); n != 0 {
				t.Fatalf("the studio launched %d run(s) beside a configured dispatcher, want 0", n)
			}
			got, err := board.Get(iss.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != native.StateReady {
				t.Fatalf("ticket state = %q, want ready (untouched)", got.State)
			}
		})
	}

	t.Run("no dispatcher launches", func(t *testing.T) {
		env := newAdmissionGateEnv(t, nil)
		iss := env.readyTicket(t)
		env.srv.admitReadyPipelines()
		if n := env.launches(); n != 1 {
			t.Fatalf("launches = %d, want 1 — a project with no dispatcher config uses the studio loop", n)
		}
		got, err := env.board.Get(iss.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != native.StateInProgress {
			t.Fatalf("ticket state = %q, want in_progress", got.State)
		}
	})
}

func TestDispatcherOwnsBoardStatus(t *testing.T) {
	cases := []struct {
		state dispatcher.ManagerState
		has   bool
		want  bool
	}{
		{dispatcher.ManagerStateIdle, false, false},
		{dispatcher.ManagerStateIdle, true, true},
		{dispatcher.ManagerStatePaused, true, true},
		{dispatcher.ManagerStateRunning, true, true},
		{dispatcher.ManagerStateError, true, true},
		{dispatcher.ManagerStateError, false, true},
	}
	for _, c := range cases {
		got := dispatcherOwnsBoardStatus(dispatcher.ManagerStatus{State: c.state, HasConfig: c.has})
		if got != c.want {
			t.Errorf("state=%s has_config=%v: owns = %v, want %v", c.state, c.has, got, c.want)
		}
	}
}

// A board launch is its own source kind (not "manual") and must not degrade
// to running in place in the project folder.
func TestAdmissionLaunchStampsPipelineBoardSource(t *testing.T) {
	env := newAdmissionGateEnv(t, nil)
	iss := env.readyTicket(t)
	env.srv.admitReadyPipelines()

	spec := env.pub.only(t)
	if spec.SourceRef == nil || spec.SourceRef.Kind != store.RunSourceKindPipelineBoard {
		t.Fatalf("source = %+v, want kind %q", spec.SourceRef, store.RunSourceKindPipelineBoard)
	}
	if spec.SourceRef.IssueID != iss.ID {
		t.Errorf("source issue = %q, want %q", spec.SourceRef.IssueID, iss.ID)
	}
	if !spec.RequireWorktree {
		t.Error("a board launch must require a worktree")
	}
}
