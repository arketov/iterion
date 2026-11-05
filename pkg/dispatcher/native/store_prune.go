package native

import (
	"time"
)

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
// When LastRunID is pruned the pointer falls back to the newest surviving
// history entry, or clears when none survives; the outcome tuple of the dead
// pointer is dropped with it (it described a run that never was).
func (s *Store) PruneRunRefs(exists func(runID string) bool, grace time.Duration) (pruned []PrunedRunRefs, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.recoverMutator("PruneRunRefs", &err)

	cutoff := time.Now().UTC().Add(-grace)
	ids := make([]string, 0, len(s.index))
	for id := range s.index {
		ids = append(ids, id)
	}
	for _, id := range ids {
		iss, rerr := s.readIssueLocked(id)
		if rerr != nil || iss.Claim != "" {
			continue
		}
		var gone []string
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
			continue
		}
		iss.Runs = kept
		if alreadyPruned(gone, iss.LastRunID) || pointerGone {
			iss.LastRunStatus, iss.LastRunError, iss.LastRunErrorCode, iss.LastRunAt = "", "", "", nil
			iss.AwaitingInput = false
			if n := len(kept); n > 0 {
				iss.LastRunID, iss.LastWorkdir = kept[n-1].RunID, kept[n-1].Workdir
			} else {
				iss.LastRunID, iss.LastWorkdir = "", ""
			}
		}
		if len(iss.Runs) == 0 {
			iss.Runs = nil
		}
		iss.UpdatedAt = time.Now().UTC()
		if werr := s.writeIssueLocked(iss); werr != nil {
			return pruned, werr
		}
		s.setIndexLocked(iss.ID, cloneIssue(iss))
		pruned = append(pruned, PrunedRunRefs{IssueID: iss.ID, RunIDs: gone})
		if eerr := s.emitPostCommitEvent(Event{
			Type:    EvtIssueLastRun,
			IssueID: iss.ID,
			Payload: map[string]any{"run_id": iss.LastRunID, "workdir": iss.LastWorkdir},
		}); eerr != nil {
			return pruned, eerr
		}
	}
	return pruned, nil
}

func alreadyPruned(gone []string, id string) bool {
	for _, g := range gone {
		if g == id {
			return true
		}
	}
	return false
}
