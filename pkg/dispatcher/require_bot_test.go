package dispatcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// requireBotDispatcher is a dispatcher with require_assignee on, whose runner
// routes the project's bots found under <dir>/bots (customy: enabled, offy:
// disabled in its manifest, my-helper: enabled with manifest dispatch_vars).
func requireBotDispatcher(t *testing.T) (*Dispatcher, *RoutingRunner) {
	t.Helper()
	c := routerTestDispatcher(t, newFakeTracker())
	dir := t.TempDir()
	botsDir := filepath.Join(dir, "bots")
	stub := "agent x:\n  model: \"test\"\n"
	writeDispatcherBot(t, filepath.Join(botsDir, "customy", "manifest.yaml"), "name: customy\n")
	writeDispatcherBot(t, filepath.Join(botsDir, "customy", "main.bot"), stub)
	writeDispatcherBot(t, filepath.Join(botsDir, "offy", "manifest.yaml"), "name: offy\nenabled: false\n")
	writeDispatcherBot(t, filepath.Join(botsDir, "offy", "main.bot"), stub)
	writeDispatcherBot(t, filepath.Join(botsDir, "my-helper", "manifest.yaml"), "name: my-helper\ndispatch_vars:\n  task: \"{{issue.title}}: {{issue.body}}\"\n")
	writeDispatcherBot(t, filepath.Join(botsDir, "my-helper", "main.bot"), stub)

	rr := &RoutingRunner{
		BotsPaths: []string{botsDir},
		compile:   func(string) (Runner, error) { return &recordingRunner{name: "dynamic"}, nil },
	}
	c.setRunner(rr)

	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.RequireAssignee = true
	cfg.Bots.Paths = []string{botsDir}
	c.cfg.Store(&cfg)
	return c, rr
}

// An assignee that is not a bot of the project — a person's name or a typo —
// is "no bot": the card is not run and the default bot is not chosen for it.
func TestDispatchRequireAssigneeRefusesAssigneeThatIsNotABot(t *testing.T) {
	c, _ := requireBotDispatcher(t)

	c.dispatch(context.Background(), tracker.Issue{ID: "i1", Identifier: "T-1", Assignee: "artem"})

	if len(c.state.running) != 0 {
		t.Fatalf("a card assigned to a non-bot must not start a run, running=%d", len(c.state.running))
	}
	sk, ok := c.state.dispatchSkips["i1"]
	if !ok || sk.Kind != DispatchSkipNoBot || sk.Assignee != "artem" || sk.Reason == "" {
		t.Fatalf("want a no_bot skip naming the assignee, got %+v ok=%v", sk, ok)
	}
}

// A bot disabled in the catalogue is refused with the reason that tells the
// operator what to switch on.
func TestDispatchRequireAssigneeRefusesDisabledBot(t *testing.T) {
	c, _ := requireBotDispatcher(t)

	c.dispatch(context.Background(), tracker.Issue{ID: "i2", Identifier: "T-2", Assignee: "offy"})

	sk, ok := c.state.dispatchSkips["i2"]
	if !ok || sk.Kind != DispatchSkipNoBot || !strings.Contains(sk.Reason, "disabled") {
		t.Fatalf("want a no_bot skip that says the bot is disabled, got %+v ok=%v", sk, ok)
	}
}

// Refusing a card with no bot reads the bot catalogue once: the route decision
// and the reason text come from the same walk. The catalogue is removed after
// that read, so a second walk (taken to tell "disabled" from "not a bot") would
// turn the disabled reason into the not-a-bot one.
func TestRefuseWithoutBotReadsTheCatalogueOnce(t *testing.T) {
	cases := []struct {
		assignee, wantReason string
	}{
		{"offy", "disabled"},
		{"artem", "not a bot"},
	}
	for _, tc := range cases {
		t.Run(tc.assignee, func(t *testing.T) {
			c, rr := requireBotDispatcher(t)
			botsDir := rr.BotsPaths[0]
			walks := 0
			rr.listBots = func(opts botregistry.ListOptions) ([]botregistry.Entry, error) {
				walks++
				entries, err := botregistry.List(opts)
				if rmErr := os.RemoveAll(botsDir); rmErr != nil {
					t.Fatalf("remove bots dir: %v", rmErr)
				}
				return entries, err
			}

			iss := tracker.Issue{ID: "once-" + tc.assignee, Identifier: "T-1", Assignee: tc.assignee}
			if !c.refuseWithoutBot(c.cfg.Load(), iss) {
				t.Fatalf("assignee %q must be refused", tc.assignee)
			}
			if walks != 1 {
				t.Errorf("catalogue read %d times for one refusal, want 1", walks)
			}
			sk, ok := c.state.dispatchSkips[iss.ID]
			if !ok || sk.Kind != DispatchSkipNoBot || !strings.Contains(sk.Reason, tc.wantReason) {
				t.Errorf("want a no_bot skip whose reason contains %q, got %+v ok=%v", tc.wantReason, sk, ok)
			}
		})
	}
}

// The same tolerant spelling routing accepts (case, `_` for `-`) is a bot, so
// the card is NOT refused here — it goes on to the claim.
func TestDispatchRequireAssigneeAcceptsRoutableBot(t *testing.T) {
	c, rr := requireBotDispatcher(t)
	for _, name := range []string{"customy", "Customy", "my_helper", "MY-HELPER"} {
		if !rr.HasRoute(name) {
			t.Fatalf("precondition: %q must route", name)
		}
		if c.refuseWithoutBot(c.cfg.Load(), tracker.Issue{ID: "ok-" + name, Identifier: "T", Assignee: name}) {
			t.Errorf("assignee %q is a bot and must not be refused", name)
		}
		if _, skipped := c.state.dispatchSkips["ok-"+name]; skipped {
			t.Errorf("assignee %q recorded a skip", name)
		}
	}
}

// Without require_assignee the documented fallback to the default workflow is
// unchanged, so an assignee that matches no bot is not refused.
func TestDispatchWithoutRequireAssigneeKeepsDefaultFallback(t *testing.T) {
	c, rr := requireBotDispatcher(t)
	rr.Default = &recordingRunner{name: "default"} // a dispatcher that has a fallback workflow
	cfg := *c.cfg.Load()
	cfg.RequireAssignee = false
	c.cfg.Store(&cfg)

	if c.refuseWithoutBot(c.cfg.Load(), tracker.Issue{ID: "i3", Identifier: "T-3", Assignee: "artem"}) {
		t.Fatal("require_assignee off: no card is refused for its assignee")
	}
}

// A plain single-workflow runner has no routing concept (no HasRoute), so an
// assignee is not judged against it.
func TestRefuseWithoutBotPlainRunnerIsNotJudged(t *testing.T) {
	c := routerTestDispatcher(t, newFakeTracker()) // StubRunner: no HasRoute
	cfg := *c.cfg.Load()
	cfg.TicketRouter = TicketRouterConfig{}
	cfg.RequireAssignee = true
	c.cfg.Store(&cfg)

	if c.refuseWithoutBot(c.cfg.Load(), tracker.Issue{ID: "i4", Identifier: "T-4", Assignee: "artem"}) {
		t.Fatal("a runner that cannot route must not refuse an assignee")
	}
}

// A card with a pending retry started under an earlier policy: its retry runs
// its course while there is a fallback workflow, and is refused when there is
// none (nothing to run it on).
func TestRefuseWithoutBotRetryException(t *testing.T) {
	c, rr := requireBotDispatcher(t)
	rr.Default = &recordingRunner{name: "default"}
	c.state.retries = map[string]*retryEntry{"i5": {}}
	iss := tracker.Issue{ID: "i5", Identifier: "T-5", Assignee: "artem"}

	cfg := *c.cfg.Load()
	cfg.Workflow = "/tmp/default.bot"
	c.cfg.Store(&cfg)
	if c.refuseWithoutBot(c.cfg.Load(), iss) {
		t.Fatal("a retrying card with a fallback workflow keeps its retry")
	}

	cfg.Workflow = ""
	c.cfg.Store(&cfg)
	rr.Default = nil
	if !c.refuseWithoutBot(c.cfg.Load(), iss) {
		t.Fatal("a retrying card with no fallback workflow has nothing to run on: refused")
	}
}

// A dispatcher built with no fallback workflow keeps refusing a card no bot
// routes to even after require_assignee is switched off by a live reload (which
// does not rebuild the runner): claiming it would only fail it on every retry.
func TestRefuseWithoutBotNoDefaultRunnerRefusesEvenWithoutRequireAssignee(t *testing.T) {
	c, _ := requireBotDispatcher(t) // its runner has no default
	cfg := *c.cfg.Load()
	cfg.RequireAssignee = false
	c.cfg.Store(&cfg)

	if !c.refuseWithoutBot(c.cfg.Load(), tracker.Issue{ID: "i6", Identifier: "T-6", Assignee: "artem"}) {
		t.Fatal("a runner with no default must refuse a card no bot routes to")
	}
	if c.refuseWithoutBot(c.cfg.Load(), tracker.Issue{ID: "i7", Identifier: "T-7", Assignee: "customy"}) {
		t.Fatal("a routable bot is still run")
	}
}

// With no fallback workflow a card no bot routes to has nothing to run: the
// runner says so with a typed error rather than a nil-pointer panic.
func TestRoutingRunnerNoDefaultReturnsErrNoBotRoute(t *testing.T) {
	rr := &RoutingRunner{BotsPaths: []string{t.TempDir()}}
	err := rr.Dispatch(context.Background(), DispatchSpec{RunID: "r1", Assignee: "artem"})
	if !errors.Is(err, ErrNoBotRoute) {
		t.Fatalf("Dispatch = %v, want ErrNoBotRoute", err)
	}
}
