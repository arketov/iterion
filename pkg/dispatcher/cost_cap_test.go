package dispatcher

import (
	"context"
	"testing"

	"github.com/arketov/iterion/pkg/clock"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/store"
)

func TestConfigLoadLimits(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
limits:
  max_cost_per_day_usd: 12.5
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.MaxCostPerDayUSD != 12.5 {
		t.Fatalf("limits.max_cost_per_day_usd = %v, want 12.5", cfg.Limits.MaxCostPerDayUSD)
	}
}

func TestConfigLimitsDefaultDisabled(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.MaxCostPerDayUSD != 0 {
		t.Fatalf("default limit = %v, want 0 (disabled)", cfg.Limits.MaxCostPerDayUSD)
	}
}

// TestBuildSpecWiresDailyCapGuard is the regression test for the
// "dispatcher cap is inert" blocker: buildSpec MUST attach a DailyCap
// guard to the spec when a limit is configured, and that guard MUST
// write to the dispatcher's SINGLETON SpendStore so dispatched runs feed
// the same ledger the refreshCostCap gate reads. Before the fix,
// EngineRunner.Dispatch built the engine without WithDailyCap, so
// dispatched runs never recorded spend and the gate never tripped.
func TestBuildSpecWiresDailyCapGuard(t *testing.T) {
	ctx := context.Background()
	d := newMinimalDispatcher(t)
	d.storeDir = t.TempDir()

	fs, err := store.New(d.storeDir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	d.spendStore = store.AsSpendStore(fs)

	cfg := &Config{
		Workflow: "/tmp/default.bot",
		Limits:   LimitsConfig{MaxCostPerDayUSD: 5.0},
	}

	spec := d.buildSpec(cfg, tracker.Issue{ID: "i-1", Title: "x"}, "run-1", "/tmp/ws", 0, nil)
	if spec.DailyCap == nil {
		t.Fatal("buildSpec must attach a DailyCap guard when a limit is configured")
	}

	// The guard records into the dispatcher's singleton ledger — the same
	// store the gate reads — so a dispatched run's spend is visible to
	// refreshCostCap. Simulate the engine's post-node Record call.
	if _, err := spec.DailyCap.Record(ctx, "run-1", 7.5); err != nil {
		t.Fatalf("DailyCap.Record: %v", err)
	}
	today := clock.DayKey(clock.Default.Now())
	ds, err := d.spendStore.LoadDailySpend(ctx, today)
	if err != nil {
		t.Fatalf("LoadDailySpend: %v", err)
	}
	if ds.SpentUSD != 7.5 {
		t.Errorf("ledger SpentUSD = %v, want 7.5 (guard must write the singleton store)", ds.SpentUSD)
	}

	// The gate, reading the same store, must now see the cap exceeded.
	d.refreshCostCap(ctx, cfg)
	if d.state.costCap == nil || !d.state.costCap.Exceeded {
		t.Fatalf("gate should see exceeded cap after a dispatched run records spend, got %+v", d.state.costCap)
	}
}

// TestBuildSpecNilDailyCapWhenDisabled confirms a zero limit leaves the
// spec's guard nil (cap disabled — WithDailyCap(nil) is inert).
func TestBuildSpecNilDailyCapWhenDisabled(t *testing.T) {
	d := newMinimalDispatcher(t)
	d.storeDir = t.TempDir()
	fs, err := store.New(d.storeDir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	d.spendStore = store.AsSpendStore(fs)

	cfg := &Config{Workflow: "/tmp/default.bot"} // Limits.MaxCostPerDayUSD == 0
	spec := d.buildSpec(cfg, tracker.Issue{ID: "i-1", Title: "x"}, "run-1", "/tmp/ws", 0, nil)
	if spec.DailyCap != nil {
		t.Errorf("buildSpec must leave DailyCap nil when no limit is configured, got %+v", spec.DailyCap)
	}
}

// TestRefreshCostCapGatesWhenExceeded exercises the dispatcher's gate
// logic: a ledger over the cap flips state.costCap.Exceeded, an override
// clears it, and a zero limit disables the cap.
func TestRefreshCostCapGatesWhenExceeded(t *testing.T) {
	ctx := context.Background()
	d := newMinimalDispatcher(t)

	fs, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	d.spendStore = store.AsSpendStore(fs)

	today := clock.DayKey(clock.Default.Now())
	if _, err := fs.AddSpend(ctx, today, "run-x", 10.0); err != nil {
		t.Fatalf("AddSpend: %v", err)
	}

	cfg := &Config{Limits: LimitsConfig{MaxCostPerDayUSD: 1.0}}
	d.refreshCostCap(ctx, cfg)
	if d.state.costCap == nil || !d.state.costCap.Exceeded {
		t.Fatalf("expected exceeded cost cap, got %+v", d.state.costCap)
	}
	if d.state.costCap.SpentUSD != 10.0 || d.state.costCap.LimitUSD != 1.0 {
		t.Errorf("costCap numbers = %+v, want spent 10 / limit 1", d.state.costCap)
	}

	// Override clears the gate.
	if _, err := fs.SetSpendOverride(ctx, today, &store.SpendOverride{Active: true, GrantedBy: "operator"}); err != nil {
		t.Fatalf("SetSpendOverride: %v", err)
	}
	d.refreshCostCap(ctx, cfg)
	if d.state.costCap.Exceeded {
		t.Errorf("override should clear Exceeded: %+v", d.state.costCap)
	}
	if !d.state.costCap.OverrideActive {
		t.Errorf("OverrideActive should be true: %+v", d.state.costCap)
	}

	// Zero limit disables the cap (status nil).
	d.refreshCostCap(ctx, &Config{})
	if d.state.costCap != nil {
		t.Errorf("disabled cap should clear status, got %+v", d.state.costCap)
	}
}
