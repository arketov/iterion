package native

import (
	"time"
)

// GhostRunGrace is how young a run-history entry must be to be left alone by
// the ghost-run cleanup: a run stamped moments ago may not have reached the
// run store yet.
const GhostRunGrace = 10 * time.Minute

// RunRefPruner is the ghost-run cleanup both board twins (native file store,
// Mongo) implement; it pins the signature and is deliberately outside
// BoardStore.
type RunRefPruner interface {
	PruneRunRefs(exists func(runID string) bool, grace time.Duration) ([]PrunedRunRefs, error)
}

var _ RunRefPruner = (*Store)(nil)

// PrunedRunRefs reports the run ids PruneRunRefs removed from one card.
type PrunedRunRefs struct {
	IssueID string
	RunIDs  []string
}

// PruneRunRefs removes from every UNCLAIMED card's run history (Runs, and the
// LastRunID pointer when it names one) the runs for which exists reports false.
//
// Why it exists: before the dispatcher stopped stamping a run id at claim time,
// a lifecycle hook that failed before the engine created the run left a
// phantom entry on the card — a run console that 404s and a diff button for a
// workspace that never ran. This is the one-off cleanup of those records.
//
// Safety: a claimed card is skipped (its run may be about to be created), and
// so is any entry younger than grace (a run stamped moments ago may not have
// reached the disk yet). exists must answer "does this run exist in the store",
// and must be conservative — returning true for anything it cannot tell.
//
// When LastRunID is pruned the pointer is CLEARED (history kept), the same as
// "Retry from zero": the dispatcher's next dispatch mints a fresh run, exactly
// what a phantom pointer already caused, instead of silently resuming an older
// failed run the card had moved past. The outcome tuple of the dead pointer is
// dropped with it (it described a run that never was).
//
// Locking: exists is a disk probe (slow on a network or OneDrive drive), so it
// is called WITHOUT s.mu, once per run id. The pass runs in three phases: a
// snapshot of the candidate ids under the lock, the disk checks without it, and
// then — only when some run was found missing — a second look at every card
// under the lock, which re-applies the claim and grace rules to the fresh card
// and treats any id it did not confirm missing as existing.
func (s *Store) PruneRunRefs(exists func(runID string) bool, grace time.Duration) (pruned []PrunedRunRefs, err error) {
	cutoff := time.Now().UTC().Add(-grace)

	// Phase 1: snapshot the run ids worth a disk check.
	toCheck := map[string]struct{}{}
	var ids []string
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, iss := range s.index {
			if iss.Claim != "" {
				continue
			}
			candidate := false
			for _, r := range iss.Runs {
				if r.RunID != "" && !r.At.After(cutoff) {
					toCheck[r.RunID] = struct{}{}
					candidate = true
				}
			}
			if iss.LastRunID != "" && (len(iss.Runs) == 0 || !iss.UpdatedAt.After(cutoff)) {
				toCheck[iss.LastRunID] = struct{}{}
				candidate = true
			}
			if candidate {
				ids = append(ids, id)
			}
		}
	}()
	if len(toCheck) == 0 {
		return nil, nil
	}

	// Phase 2: the disk, without the store lock.
	missing := map[string]bool{}
	for runID := range toCheck {
		if !exists(runID) {
			missing[runID] = true
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	// An id not confirmed missing (e.g. added after the snapshot) counts as
	// existing.
	confirmedExists := func(runID string) bool { return !missing[runID] }

	// Phase 3: re-check each card against its fresh state and write.
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.recoverMutator("PruneRunRefs", &err)

	for _, id := range ids {
		iss, rerr := s.readIssueLocked(id)
		if rerr != nil || iss.Claim != "" {
			continue
		}
		gone, pointerChanged := PruneGhostRunRefs(iss, confirmedExists, cutoff)
		if len(gone) == 0 {
			continue
		}
		// UpdatedAt is left alone: it orders the dispatch queue and the board,
		// and a housekeeping rewrite is not an edit of the card.
		if werr := s.writeIssueLocked(iss); werr != nil {
			return pruned, werr
		}
		s.setIndexLocked(iss.ID, cloneIssue(iss))
		pruned = append(pruned, PrunedRunRefs{IssueID: iss.ID, RunIDs: gone})
		if pointerChanged {
			_ = s.emitPostCommitEvent(Event{
				Type:    EvtIssueLastRun,
				IssueID: iss.ID,
				Payload: map[string]any{"run_id": iss.LastRunID, "workdir": iss.LastWorkdir},
			})
		}
	}
	return pruned, nil
}

// PruneGhostRunRefs is the one definition of what the ghost-run cleanup removes
// from a single card, shared by both board twins. It drops from iss.Runs every
// entry with an id, not newer than cutoff, for which exists reports false, and
// clears the LastRunID pointer (with its outcome tuple) when it names a gone
// run. gone lists every removed run id; pointerCleared reports that the pointer
// fields were reset. It touches neither UpdatedAt nor the claim: the caller
// skips claimed cards itself.
func PruneGhostRunRefs(iss *Issue, exists func(runID string) bool, cutoff time.Time) (gone []string, pointerCleared bool) {
	kept := make([]RunRef, 0, len(iss.Runs))
	for _, r := range iss.Runs {
		if r.RunID != "" && !r.At.After(cutoff) && !exists(r.RunID) {
			gone = append(gone, r.RunID)
			continue
		}
		kept = append(kept, r)
	}
	// The single pointer may name a run the history never carried
	// (records older than run history).
	pointerGone := false
	if iss.LastRunID != "" && !alreadyPruned(gone, iss.LastRunID) && !exists(iss.LastRunID) &&
		(len(iss.Runs) == 0 || !iss.UpdatedAt.After(cutoff)) {
		pointerGone = true
		gone = append(gone, iss.LastRunID)
	}
	if len(gone) == 0 {
		return nil, false
	}
	pointerCleared = alreadyPruned(gone, iss.LastRunID) || pointerGone
	iss.Runs = kept
	if pointerCleared {
		iss.LastRunStatus, iss.LastRunError, iss.LastRunErrorCode, iss.LastRunAt = "", "", "", nil
		iss.AwaitingInput = false
		iss.LastRunID, iss.LastWorkdir = "", ""
	}
	if len(iss.Runs) == 0 {
		iss.Runs = nil
	}
	return gone, pointerCleared
}

func alreadyPruned(gone []string, id string) bool {
	for _, g := range gone {
		if g == id {
			return true
		}
	}
	return false
}
