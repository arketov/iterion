package runtime

import (
	"context"

	"github.com/arketov/iterion/pkg/clock"
	"github.com/arketov/iterion/pkg/store"
)

// CapReasonDaily is the sentinel value written to the run_paused event's
// "reason" field when a run is paused by the per-day spend cap. The
// studio keys its cost-cap banner off this; resume treats the run like
// any other paused_operator run.
const CapReasonDaily = "cost_cap_daily"

// DailyCapConfig configures the per-(store, UTC-day) spend cap.
type DailyCapConfig struct {
	// MaxCostPerDayUSD is the daily LLM spend ceiling in USD. Zero (or
	// negative) disables the cap.
	MaxCostPerDayUSD float64
}

// CapStatus is the read-only view of the daily cap at a point in time.
type CapStatus struct {
	Enabled        bool    `json:"enabled"`
	Date           string  `json:"date"`
	SpentUSD       float64 `json:"spent_usd"`
	LimitUSD       float64 `json:"limit_usd"`
	Exceeded       bool    `json:"exceeded"`
	OverrideActive bool    `json:"override_active"`
}

// DailyCapGuard is the shared spend-cap engine consumed by both the
// runtime engine (per-run accounting + pause) and the dispatcher
// (gate new dispatches + surface status). It is safe for concurrent
// use — the underlying SpendStore serialises writes.
//
// A nil *DailyCapGuard is a valid "cap disabled" value: every method is
// nil-receiver-safe, so call sites need no nil checks.
type DailyCapGuard struct {
	store store.SpendStore
	clk   clock.Clock
	cfg   DailyCapConfig
}

// NewDailyCapGuard builds a guard. Returns nil (cap disabled) when the
// store is nil or the limit is non-positive — callers can pass the
// result straight to WithDailyCap.
func NewDailyCapGuard(sink store.SpendStore, clk clock.Clock, cfg DailyCapConfig) *DailyCapGuard {
	if sink == nil || cfg.MaxCostPerDayUSD <= 0 {
		return nil
	}
	if clk == nil {
		clk = clock.Default
	}
	return &DailyCapGuard{store: sink, clk: clk, cfg: cfg}
}

// today returns the current UTC day key.
func (g *DailyCapGuard) today() string { return clock.DayKey(g.clk.Now()) }

func (g *DailyCapGuard) statusFrom(ds *store.DailySpend) CapStatus {
	override := ds.Override != nil && ds.Override.Active
	return CapStatus{
		Enabled:        true,
		Date:           ds.Date,
		SpentUSD:       ds.SpentUSD,
		LimitUSD:       g.cfg.MaxCostPerDayUSD,
		Exceeded:       ds.SpentUSD >= g.cfg.MaxCostPerDayUSD && !override,
		OverrideActive: override,
	}
}

// Status returns the current cap status for today (read-only). A nil
// guard reports a disabled cap.
func (g *DailyCapGuard) Status(ctx context.Context) (CapStatus, error) {
	if g == nil {
		return CapStatus{}, nil
	}
	date := g.today()
	ds, err := g.store.LoadDailySpend(ctx, date)
	if err != nil {
		return CapStatus{Enabled: true, Date: date, LimitUSD: g.cfg.MaxCostPerDayUSD}, err
	}
	return g.statusFrom(ds), nil
}

// Record persists run runID's latest cumulative cost into today's ledger
// and returns the resulting status. A nil guard is a no-op.
func (g *DailyCapGuard) Record(ctx context.Context, runID string, cumulativeRunCostUSD float64) (CapStatus, error) {
	if g == nil {
		return CapStatus{}, nil
	}
	date := g.today()
	ds, err := g.store.AddSpend(ctx, date, runID, cumulativeRunCostUSD)
	if err != nil {
		return CapStatus{Enabled: true, Date: date, LimitUSD: g.cfg.MaxCostPerDayUSD}, err
	}
	return g.statusFrom(ds), nil
}

// SetOverride sets (active=true) or clears (active=false) today's
// override flag and returns the resulting status. grantedBy/note are
// recorded for audit. A nil guard is a no-op.
func (g *DailyCapGuard) SetOverride(ctx context.Context, active bool, grantedBy, note string) (CapStatus, error) {
	if g == nil {
		return CapStatus{}, nil
	}
	date := g.today()
	var ov *store.SpendOverride
	if active {
		ov = &store.SpendOverride{Active: true, GrantedAt: g.clk.Now(), GrantedBy: grantedBy, Note: note}
	}
	ds, err := g.store.SetSpendOverride(ctx, date, ov)
	if err != nil {
		return CapStatus{Enabled: true, Date: date, LimitUSD: g.cfg.MaxCostPerDayUSD}, err
	}
	return g.statusFrom(ds), nil
}
