// Package server — memoization for the runs-stats aggregation.
//
// The /api/v1/runs/stats handler walks every recent run's events.jsonl
// (one store.ScanEvents per run) to sum per-node `_cost_usd`. That walk
// is the expensive part of the dashboard: a 30-day store with hundreds
// of runs re-reads every log on each Refresh, and the studio's window
// chips (7/14/30/90d) multiply that by repeated loads of overlapping
// run sets.
//
// runStatsCache memoizes the per-run cost-by-day map — the only
// expensive, run-scoped output of the aggregation. Duration and status
// come from the in-memory RunSummary (already loaded by ListCtx) and are
// cheap, so they are not cached.
//
// Why terminal-runs-only, version-keyed:
//
//   - store.AppendEvent does NOT bump run.UpdatedAt, so a cache keyed on
//     UpdatedAt would serve stale cost for an in-flight run that keeps
//     appending node_finished events. We therefore cache ONLY terminal
//     runs (RunStatus.IsTerminal): once a run reaches finished / failed /
//     failed_resumable / cancelled it appends no further events, so its
//     cost-by-day map is immutable. Non-terminal runs are never cached
//     and are re-scanned on every request — they are few, and this keeps
//     the live numbers honest. This is the implicit "invalidate on
//     event-write" guarantee: a cached entry corresponds to a run that
//     receives no further writes.
//
//   - The lone path that mutates an already-terminal run is
//     `iterion resume --force` re-finishing it, which advances UpdatedAt.
//     We fold UpdatedAt (and Status) into the version key so that case
//     busts the cache automatically.
//
// The key is built from RunSummary fields, not a file stat, so the cache
// is store-agnostic (works for the Mongo/cloud store too).

package server

import (
	"strconv"
	"sync"

	"github.com/arketov/iterion/pkg/runview"
)

// perRunCostEntry is one memoized run: the cost-by-day map and the run
// version it was computed at. byDay is treated as read-only once stored.
type perRunCostEntry struct {
	version string
	byDay   map[string]float64
}

// runStatsCache is a tiny version-keyed memo of per-run cost-by-day maps.
// Safe for concurrent use. The mutex guards only map access; it is never
// held across a store.ScanEvents file walk (see cachedRunCostByDay).
type runStatsCache struct {
	mu      sync.Mutex
	entries map[string]perRunCostEntry
}

func newRunStatsCache() *runStatsCache {
	return &runStatsCache{entries: make(map[string]perRunCostEntry)}
}

// get returns the cached cost-by-day map for runID, but only when the
// stored version matches the caller's version (a stale entry counts as a
// miss). The returned map must NOT be mutated by the caller.
func (c *runStatsCache) get(runID, version string) (map[string]float64, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[runID]
	if !ok || e.version != version {
		return nil, false
	}
	return e.byDay, true
}

// put stores the cost-by-day map for (runID, version), replacing any
// prior entry for that run. Callers store only terminal runs.
func (c *runStatsCache) put(runID, version string, byDay map[string]float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[runID] = perRunCostEntry{version: version, byDay: byDay}
}

// clear drops every entry. Called on a project switch, where the run set
// changes wholesale and old per-run cost would otherwise linger (and the
// map grow unbounded across switches).
func (c *runStatsCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]perRunCostEntry)
}

// runVersion derives the cache version for a run. The status transition
// into a terminal state stamps a fresh UpdatedAt (UpdateRunStatus /
// FailRunResumable), and resuming a run re-runs that transition, so
// UpdatedAt moves whenever a cached terminal run could have gained new
// cost. Status is folded in as a second guard against an UpdatedAt
// collision.
func runVersion(r *runview.RunSummary) string {
	return strconv.FormatInt(r.UpdatedAt.UnixNano(), 10) + "|" + string(r.Status)
}
