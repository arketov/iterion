package boardmongo

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

var _ native.RunRefPruner = (*Store)(nil)

// ghostRunCandidates selects the cards that carry a run reference at all and
// are unclaimed (a claimed card's run may be about to be created).
func (s *Store) ghostRunCandidates() bson.M {
	return bson.M{
		"tenant_id":   s.tenant,
		"issue.claim": bson.M{"$in": bson.A{"", nil}},
		"$or":         ghostRunRefFilter(),
	}
}

// ghostRunRefFilter matches a card that records a run: a non-empty history or
// a LastRunID pointer.
func ghostRunRefFilter() bson.A {
	return bson.A{
		bson.M{"issue.runs.0": bson.M{"$exists": true}},
		bson.M{"issue.lastrunid": bson.M{"$gt": ""}},
	}
}

// PruneRunRefs is the Mongo twin of native.Store.PruneRunRefs: it removes from
// every UNCLAIMED card's run history (and the LastRunID pointer, with its
// outcome tuple) the runs for which exists reports false. What goes is decided
// by native.PruneGhostRunRefs, the one definition both twins share.
//
// Unlike the file store there is no single critical section: each card is
// written with a CAS guard (still unclaimed, updatedat unchanged since it was
// read), so several replicas pruning at once cannot clobber a card a
// dispatcher claimed or edited meanwhile — a card that moved is skipped and
// the next boot retries it. A tenant-less store does nothing: a run cannot be
// probed without a tenant.
func (s *Store) PruneRunRefs(exists func(runID string) bool, grace time.Duration) ([]native.PrunedRunRefs, error) {
	if s.tenant == "" {
		return nil, nil
	}
	parent := sweepCtx()
	ctx, cancel := callCtx(parent)
	cur, err := s.issues.Find(ctx, s.ghostRunCandidates())
	if err != nil {
		cancel()
		return nil, fmt.Errorf("boardmongo: list cards with run refs: %w", err)
	}
	var docs []issueDoc
	err = cur.All(ctx, &docs)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("boardmongo: decode cards with run refs: %w", err)
	}

	cutoff := time.Now().UTC().Add(-grace)
	var pruned []native.PrunedRunRefs
	for _, doc := range docs {
		observed := doc.Issue.UpdatedAt
		iss := doc.Issue
		iss.Runs = append([]native.RunRef(nil), doc.Issue.Runs...)
		gone, pointerCleared := native.PruneGhostRunRefs(&iss, exists, cutoff)
		if len(gone) == 0 {
			continue
		}
		changed := []string{"runs"}
		if pointerCleared {
			changed = append(changed, "lastrunid", "lastworkdir", "lastrunstatus", "lastrunerror",
				"lastrunerrorcode", "lastrunat", "awaitinginput")
		}
		guard := bson.M{"issue.claim": bson.M{"$in": bson.A{"", nil}}, "issue.updatedat": observed}
		matched, err := s.cascadeReplaceGuarded(parent, &iss, guard, changed...)
		if err != nil {
			return pruned, err
		}
		if !matched {
			continue
		}
		pruned = append(pruned, native.PrunedRunRefs{IssueID: iss.ID, RunIDs: gone})
		if pointerCleared {
			// A projection hint, not data: a failed emit is not worth failing the sweep.
			_ = s.emit(native.Event{
				Type:    native.EvtIssueLastRun,
				IssueID: iss.ID,
				Payload: map[string]any{"run_id": "", "workdir": ""},
			})
		}
	}
	return pruned, nil
}
