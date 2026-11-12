package dispatcher

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// prRouterTracker wraps the plain fakeTracker and implements the OPTIONAL
// linkedPRProbe + labelApplier capabilities the ticket router type-asserts.
// hasPR is canned per test; applied labels are recorded.
type prRouterTracker struct {
	*fakeTracker
	hasPR bool

	mu     sync.Mutex
	labels map[string]string // issueID -> last label applied
}

func newPRRouterTracker(hasPR bool) *prRouterTracker {
	return &prRouterTracker{fakeTracker: newFakeTracker(), hasPR: hasPR, labels: map[string]string{}}
}

func (t *prRouterTracker) HasLinkedPR(_ context.Context, _ string) (bool, error) { return t.hasPR, nil }

func (t *prRouterTracker) ApplyLabel(_ context.Context, id, label string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.labels[id] = label
	return nil
}

func (t *prRouterTracker) labelFor(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.labels[id]
}

func routerTestDispatcher(t *testing.T, ft tracker.Tracker) *Dispatcher {
	t.Helper()
	dir := t.TempDir()
	wsDir := dir + "/ws"
	cfg := &Config{
		Name:         "test",
		Workflow:     t.TempDir() + "/fake.bot",
		Tracker:      TrackerConfig{Kind: "fake"},
		Polling:      PollingConfig{IntervalMS: 100},
		Agent:        AgentConfig{MaxConcurrent: 4, MaxRetryBackoffMS: 1000},
		Workspace:    WorkspaceConfig{Root: wsDir},
		Stall:        StallConfig{},
		TicketRouter: TicketRouterConfig{Enabled: true},
	}
	cfg.applyDefaults()
	ws, err := NewWorkspaces(wsDir)
	if err != nil {
		t.Fatalf("NewWorkspaces: %v", err)
	}
	c, err := New(Options{
		Config:     cfg,
		Tracker:    ft,
		Runner:     &StubRunner{},
		Workspaces: ws,
		Logger:     iterlog.New(iterlog.LevelError, &bytes.Buffer{}),
		HostMarker: "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestRouteUnassignedIssue_NoPR: an unassigned issue with no linked PR routes to
// the implement bot (Featurly) and is stamped bot:featurly.
func TestRouteUnassignedIssue_NoPR(t *testing.T) {
	ft := newPRRouterTracker(false)
	c := routerTestDispatcher(t, ft)
	iss := tracker.Issue{ID: "github:acme/widgets#5", Identifier: "widgets#5"}

	bot, ok := c.routeUnassignedIssue(context.Background(), c.cfg.Load(), iss)
	if !ok || bot != "feature-dev" {
		t.Fatalf("no-PR issue must route to feature-dev: bot=%q ok=%v", bot, ok)
	}
	if got := ft.labelFor(iss.ID); got != "bot:featurly" {
		t.Fatalf("expected bot:featurly label, got %q", got)
	}
}

// TestRouteUnassignedIssue_HasPR: an unassigned issue a PR already links makes
// the dispatcher STEP ASIDE (ok=false) — the PR-webhook owns Billy-on-PR — and
// the issue is stamped bot:billy for visibility.
func TestRouteUnassignedIssue_HasPR(t *testing.T) {
	ft := newPRRouterTracker(true)
	c := routerTestDispatcher(t, ft)
	iss := tracker.Issue{ID: "github:acme/widgets#6", Identifier: "widgets#6"}

	bot, ok := c.routeUnassignedIssue(context.Background(), c.cfg.Load(), iss)
	if ok || bot != "" {
		t.Fatalf("PR-linked issue must step aside: bot=%q ok=%v", bot, ok)
	}
	if got := ft.labelFor(iss.ID); got != "bot:billy" {
		t.Fatalf("expected bot:billy label, got %q", got)
	}
}

// TestRouteUnassignedIssue_NoProbe: a tracker WITHOUT the linked-PR capability
// degrades to the implement bot (never blocks an issue, never dedups against a
// PR-webhook it cannot observe).
func TestRouteUnassignedIssue_NoProbe(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker()) // plain fake: no HasLinkedPR
	iss := tracker.Issue{ID: "x#1", Identifier: "x#1"}

	bot, ok := c.routeUnassignedIssue(context.Background(), c.cfg.Load(), iss)
	if !ok || bot != "feature-dev" {
		t.Fatalf("no-probe tracker must degrade to feature-dev: bot=%q ok=%v", bot, ok)
	}
}

// TestRouteUnassignedIssue_NoProbeWarnsOnce: an enabled router on a tracker
// that cannot probe linked PRs (native, forgejo) sends every unassigned card to
// the implement bot, outranking require_assignee. The operator is told exactly
// once per process — not silently, not on every card.
func TestRouteUnassignedIssue_NoProbeWarnsOnce(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker()) // plain fake: no HasLinkedPR
	var buf bytes.Buffer
	c.logger = iterlog.New(iterlog.LevelWarn, &buf)

	for _, id := range []string{"x#1", "x#2", "x#3"} {
		iss := tracker.Issue{ID: id, Identifier: id}
		if bot, ok := c.routeUnassignedIssue(context.Background(), c.cfg.Load(), iss); !ok || bot != "feature-dev" {
			t.Fatalf("no-probe tracker must route to feature-dev: bot=%q ok=%v", bot, ok)
		}
	}
	if n := strings.Count(buf.String(), "ticket_router"); n != 1 {
		t.Fatalf("expected exactly one ticket_router no-probe WARN, got %d in %q", n, buf.String())
	}
}

// TestDispatchRouterOutranksRequireAssignee pins the owner's decision (docs/adr/169-ticket-router-outranks-require-assignee-on-trackers-without-pr-probe.md):
// with ticket_router on, an unassigned card is NOT refused by require_assignee —
// the router names the implement bot first, so dispatch proceeds to bot
// resolution (and honest-fails there on an unresolvable bot) instead of
// recording a "no_assignee" skip.
func TestDispatchRouterOutranksRequireAssignee(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker())
	cfg := *c.cfg.Load()
	cfg.RequireAssignee = true
	cfg.Bots.Paths = []string{t.TempDir()} // empty catalogue: feature-dev cannot resolve
	c.cfg.Store(&cfg)
	iss := tracker.Issue{ID: "x#9", Identifier: "x#9"}

	c.dispatch(context.Background(), iss)

	skip, ok := c.state.dispatchSkips[iss.ID]
	if !ok {
		t.Fatal("expected a dispatch skip from bot resolution")
	}
	if skip.Kind == DispatchSkipNoAssignee {
		t.Fatalf("router must outrank require_assignee, got a no_assignee skip: %+v", skip)
	}
	if skip.Bot != "feature-dev" {
		t.Fatalf("router must name the implement bot before the guards, got bot=%q", skip.Bot)
	}
}
