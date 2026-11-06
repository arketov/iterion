package server

import (
	"context"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/store"
)

// ghostRunProbeTimeout bounds one run-existence probe of the ghost-run cleanup.
const ghostRunProbeTimeout = 10 * time.Second

// runStillExists is the one predicate of the cloud ghost-run cleanup: a run is
// gone only when the run store PROVES it (store.RunAbsent — not found or
// tombstoned). Any other error, a timeout included, means "cannot tell", and
// the card's record is kept: the cleanup is irreversible, so it errs towards
// leaving a dead link over losing a live one.
func runStillExists(loadErr error) bool { return !store.RunAbsent(loadErr) }

// pruneCloudGhostRunRefs is the cloud twin of the native boot-time ghost-run
// cleanup (dispatcher.Manager.pruneGhostRunsAtBoot): for every tenant with a
// card that records a run it drops, from unclaimed cards, the history entries
// (and the pointer) whose run the run store reports absent. Best-effort: a
// failure is logged at warn and never blocks boot.
//
// It is a one-off pass without a lease on purpose: every write is a CAS on
// the card still being unclaimed and unchanged, so replicas starting together
// at worst probe the same runs twice, and a restart repeats it harmlessly.
func (s *Server) pruneCloudGhostRunRefs(ctx context.Context) {
	coord := s.cfg.CloudBoardCoordinator
	if coord == nil || s.runs == nil {
		return
	}
	tenants, err := coord.DistinctCardTenants(ctx)
	if err != nil {
		s.warnf("board: ghost-run cleanup failed to list tenants: %v", err)
		return
	}
	for _, tenant := range tenants {
		if ctx.Err() != nil {
			return
		}
		if tenant == "" {
			continue // a run cannot be probed without a tenant
		}
		exists := func(runID string) bool {
			pctx, cancel := context.WithTimeout(store.WithIdentity(ctx, tenant, boardDispatcherActor), ghostRunProbeTimeout)
			defer cancel()
			_, err := s.runs.LoadRunCtx(pctx, runID)
			return runStillExists(err)
		}
		pruned, err := coord.StoreFor(tenant).PruneRunRefs(exists, native.GhostRunGrace)
		if err != nil {
			s.warnf("board: ghost-run cleanup failed (tenant %s): %v", tenant, err)
			continue
		}
		if len(pruned) > 0 {
			total := 0
			for _, p := range pruned {
				total += len(p.RunIDs)
			}
			s.infof("board: removed %d run(s) that never started from the history of %d card(s) (tenant %s)", total, len(pruned), tenant)
		}
	}
}
