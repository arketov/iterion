package dispatcher

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/store"
)

// slotResumeFixture builds a dispatcher over a native tracker whose card sits in
// in_progress with a persisted last run of the given status and claimed-from
// column — the state a studio restart leaves behind — with review capped at 1.
func slotResumeFixture(t *testing.T, status store.RunStatus, claimedFrom string) (*Dispatcher, *Config, tracker.Issue) {
	t.Helper()
	dir := t.TempDir()
	runStore, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	run, err := runStore.CreateRun(context.Background(), "run-1", "wf", nil)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	run.Status = status
	if status == store.RunStatusFailedResumable {
		run.FailureCode = store.FailureProcessOrphaned
	}
	run.Source = &store.RunSource{Kind: store.RunSourceKindDispatcher, IssueID: "native:x", ClaimedFromState: claimedFrom}
	if err := runStore.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	ns, err := native.NewStore(filepath.Join(dir, "dispatcher"))
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	card, err := ns.Create(native.Issue{Title: "resumed", State: native.StateInProgress})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ns.SetLastRun(card.ID, "run-1", ""); err != nil {
		t.Fatalf("SetLastRun: %v", err)
	}

	cfg := &Config{
		Name:     "test",
		Workflow: filepath.Join(t.TempDir(), "fake.bot"),
		Tracker:  TrackerConfig{Kind: "native"},
		Polling:  PollingConfig{IntervalMS: 50},
		Agent: AgentConfig{
			MaxConcurrent:        4,
			RunningState:         "in_progress",
			MaxConcurrentByState: map[string]int{"review": 1},
		},
		Workspace: WorkspaceConfig{Root: filepath.Join(dir, "ws")},
	}
	cfg.applyDefaults()
	ws, err := NewWorkspaces(cfg.Workspace.Root)
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	c, err := New(Options{
		Config:     cfg,
		Tracker:    native.NewAdapter(ns),
		Runner:     &StubRunner{},
		Workspaces: ws,
		Logger:     iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test-host-1",
		StoreDir:   dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, cfg, tracker.Issue{ID: card.ID, Identifier: "T-1", WorkflowState: "in_progress"}
}

func TestSlotStateFor_ResumedRunChargesColumnOfFirstCapture(t *testing.T) {
	c, cfg, iss := slotResumeFixture(t, store.RunStatusFailedResumable, "review")

	if got := c.slotStateFor(cfg, iss); got != "review" {
		t.Fatalf("slotStateFor = %q, want review (the column the run was first claimed from)", got)
	}
	if !c.hasSlot(c.slotStateFor(cfg, iss), cfg) {
		t.Errorf("review slot is free: the resumed card must be dispatchable")
	}
	c.state.slotsByState["review"] = 1
	if c.hasSlot(c.slotStateFor(cfg, iss), cfg) {
		t.Errorf("review slot is taken: the resumed card must wait, not slip through on in_progress")
	}
}

func TestSlotStateFor_LegacyRunWithoutColumnFallsBack(t *testing.T) {
	c, cfg, iss := slotResumeFixture(t, store.RunStatusFailedResumable, "")
	if got := c.slotStateFor(cfg, iss); got != "in_progress" {
		t.Fatalf("slotStateFor = %q, want in_progress for a run without claimed_from_state", got)
	}
}

func TestSlotStateFor_FinishedLastRunIsFreshCard(t *testing.T) {
	c, cfg, iss := slotResumeFixture(t, store.RunStatusFinished, "review")
	if got := c.slotStateFor(cfg, iss); got != "in_progress" {
		t.Fatalf("slotStateFor = %q, want in_progress: a finished last run is not resumed", got)
	}
}

func TestSlotStateFor_OtherColumnKeepsItsOwnSlot(t *testing.T) {
	c, cfg, iss := slotResumeFixture(t, store.RunStatusFailedResumable, "review")
	iss.WorkflowState = "ready"
	if got := c.slotStateFor(cfg, iss); got != "ready" {
		t.Fatalf("slotStateFor = %q, want ready: only a card in running_state resumes under another column's slot", got)
	}
}

func TestBuildRunningEntry_ResumedRunHoldsColumnSlotAndKeepsWorkflowState(t *testing.T) {
	ft := newFakeTracker()
	c := newTestDispatcher(t, &StubRunner{}, ft, time.Hour)
	iss := tracker.Issue{ID: "fake:1", Identifier: "fake#1", WorkflowState: "in_progress"}

	entry := c.buildRunningEntry(iss, "r1", "", false, 0, "review", func(error) {})
	if entry.WorkflowState != "in_progress" {
		t.Errorf("WorkflowState = %q, want in_progress (refreshRunningStates compares against it)", entry.WorkflowState)
	}
	if c.state.slotsByState["review"] != 1 || c.state.slotsByState["in_progress"] != 0 {
		t.Fatalf("slotsByState = %v, want review=1 and no in_progress", c.state.slotsByState)
	}

	ft.claims["fake:1"] = c.hostMarker
	c.finishRun(context.Background(), "fake:1", fmt.Errorf("%w: run cancelled by user", runtime.ErrRunCancelled))
	if len(c.state.slotsByState) != 0 {
		t.Errorf("slotsByState = %v after finishRun, want empty (the review slot is released)", c.state.slotsByState)
	}
}

func TestBuildSpec_StampsSlotColumnAsClaimedFromState(t *testing.T) {
	c := newTestDispatcher(t, &StubRunner{}, newFakeTracker(), time.Hour)
	cfg := c.cfg.Load()
	iss := tracker.Issue{ID: "fake:1", Identifier: "fake#1", WorkflowState: "review"}

	entry := c.buildRunningEntry(iss, "r1", "", false, 0, "review", func(error) {})
	spec := c.buildSpec(cfg, iss, "r1", entry.WorkspacePath, 0, entry)
	if spec.Issue == nil || spec.Issue.ClaimedFromState != "review" {
		t.Fatalf("spec.Issue = %+v, want ClaimedFromState review", spec.Issue)
	}
}
