package dispatcher

import (
	"context"
	"fmt"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	"github.com/arketov/iterion/pkg/store"
)

// sourceHold is the in-memory refusal the dispatcher keeps for a card whose
// last_run could not be resumed because the bot source changed since the run
// started. It is NOT a pause: nobody has anything to answer, so the card stays
// in the column it was taken from (no claim, no awaiting_input), and the hold —
// keyed by the run id — stops dispatch before Claim so there is no
// Claim/Release churn and no repeated refusal on every tick.
//
// Actor-goroutine-owned. In-memory by design: a daemon restart loses it and
// costs exactly one extra claim → refusal → hold cycle (no durable state, so no
// cloud twin).
type sourceHold struct {
	identifier string
	runID      string
	// sourceState is the column the card was taken from (r.TransitionedFromState).
	sourceState string
	// adopted: the run was continued from outside (resume --force from the run
	// console) and the card was moved to RunningState to follow it.
	adopted bool
}

// hasLastRunSeam reports whether the tracker persists the last_run pointer
// (native only). The hold is released by a changed/cleared pointer or by the
// run's own status, so a pointer-less tracker (github/forgejo) cannot use it.
func (c *Dispatcher) hasLastRunSeam() bool {
	_, ok := c.tracker.(interface {
		LastRunForIssue(id string) (string, error)
	})
	return ok
}

// sourceChangedReason is the operator-facing text of the source_changed skip.
func sourceChangedReason(runID string) string {
	return fmt.Sprintf("bot source changed since run %s started — the dispatcher does not resume it automatically. Continue it with the new bot version (resume --force) or start over (reset last run: `iterion issue update --clear-last-run`)", runID)
}

// holdSourceChanged registers the hold and its dashboard skip. The caller has
// already stamped last_run, so the pointer equals r.RunID.
func (c *Dispatcher) holdSourceChanged(issueID string, r *runningEntry) {
	if c.state.sourceHolds == nil {
		c.state.sourceHolds = map[string]*sourceHold{}
	}
	c.state.sourceHolds[issueID] = &sourceHold{
		identifier:  r.Identifier,
		runID:       r.RunID,
		sourceState: r.TransitionedFromState,
	}
	c.recordDispatchSkipKind(tracker.Issue{ID: issueID, Identifier: r.Identifier},
		DispatchSkipSourceChanged, sourceChangedReason(r.RunID))
	c.logger.Warn("dispatcher: %s bot source changed (run=%s) — refusing a fresh sibling; card returned to %q, held until force-resume or reset last_run.", r.Identifier, r.RunID, r.TransitionedFromState)
}

// sourceHoldBeforeClaim is the pre-claim check of dispatch: true means the card
// is held and must not be claimed. prev is the card's current last_run pointer;
// a pointer that no longer equals the held run (cleared by "Start over" /
// --clear-last-run, or replaced) drops the hold and lets dispatch go on.
func (c *Dispatcher) sourceHoldBeforeClaim(iss tracker.Issue, prev string) bool {
	h, ok := c.state.sourceHolds[iss.ID]
	if !ok {
		return false
	}
	if prev != h.runID {
		delete(c.state.sourceHolds, iss.ID)
		return false
	}
	if !h.adopted {
		c.recordDispatchSkipKind(iss, DispatchSkipSourceChanged, sourceChangedReason(h.runID))
	}
	return true
}

// dropSourceHold forgets the hold and its source_changed skip.
func (c *Dispatcher) dropSourceHold(issueID string) {
	delete(c.state.sourceHolds, issueID)
	if s, ok := c.state.dispatchSkips[issueID]; ok && s.Kind == DispatchSkipSourceChanged {
		delete(c.state.dispatchSkips, issueID)
	}
}

// reconcileSourceHolds follows held cards through what the operator does
// outside the dispatcher: a force-resume moves the card to in_progress and
// clears the badge; the run's end moves it to its terminal column; a reset of
// last_run drops the hold. Local disk I/O only, safe on the actor.
func (c *Dispatcher) reconcileSourceHolds(ctx context.Context) {
	if len(c.state.sourceHolds) == 0 {
		return
	}
	rs, err := c.openRunStore()
	if err != nil {
		return
	}
	cfg := c.cfg.Load()
	moved := false
	for id, h := range c.state.sourceHolds {
		if _, running := c.state.running[id]; running {
			continue
		}
		if c.lastRunID(id) != h.runID {
			c.dropSourceHold(id)
			continue
		}
		status := c.runStatusFrom(rs, h.runID)
		if status == "" {
			continue
		}
		switch status {
		case store.RunStatusRunning, store.RunStatusQueued:
			if h.adopted {
				continue
			}
			if !c.moveHeldCard(ctx, id, h.identifier, h.sourceState, cfg.Agent.RunningState) {
				if cur := c.cardState(ctx, id); cur != "" && cur != h.sourceState {
					c.dropSourceHold(id) // dragged elsewhere by the operator
				}
				continue
			}
			c.setAwaitingInput(id, false, nil)
			if s, ok := c.state.dispatchSkips[id]; ok && s.Kind == DispatchSkipSourceChanged {
				delete(c.state.dispatchSkips, id)
			}
			h.adopted = true
			moved = true
			c.logger.Info("dispatcher: %s run %s was resumed out-of-band — card moved to %q and followed to its end", h.identifier, h.runID, cfg.Agent.RunningState)
		case store.RunStatusFinished, store.RunStatusFailed:
			target := cfg.Agent.CompletedState
			if status == store.RunStatusFailed {
				target = cfg.Agent.FailedState
			}
			if target != "" {
				from := h.sourceState
				if h.adopted {
					from = cfg.Agent.RunningState
				}
				if c.moveHeldCard(ctx, id, h.identifier, from, target) {
					moved = true
				}
			}
			c.dropSourceHold(id)
			c.setAwaitingInput(id, false, nil)
		case store.RunStatusPausedWaitingHuman, store.RunStatusPausedOperator:
			// reconcileStrandedPaused re-parks it in the same tick.
			c.dropSourceHold(id)
		case store.RunStatusFailedResumable:
			if !h.adopted {
				continue
			}
			// The forced resume failed again: give the card back; the next
			// dispatch pass restores the badge.
			if c.moveHeldCard(ctx, id, h.identifier, cfg.Agent.RunningState, h.sourceState) {
				h.adopted = false
				moved = true
			}
		case store.RunStatusCancelled:
			if h.adopted {
				if c.moveHeldCard(ctx, id, h.identifier, cfg.Agent.RunningState, h.sourceState) {
					moved = true
				}
			}
			// The existing "cancelled by the operator" hold takes over.
			c.dropSourceHold(id)
		}
	}
	if moved {
		c.fireSnapshot()
	}
}

// cardState is the card's current tracker state, "" when unreadable.
func (c *Dispatcher) cardState(ctx context.Context, id string) string {
	states, err := c.tracker.RefreshStates(ctx, []string{id})
	if err != nil {
		return ""
	}
	return states[id]
}

// moveHeldCard moves a held card from → to, only when it is still in from
// (the same guard as revertTransition: never clobber an operator's drag).
func (c *Dispatcher) moveHeldCard(ctx context.Context, id, identifier, from, to string) bool {
	if from == "" || to == "" || from == to {
		return false
	}
	if c.cardState(ctx, id) != from {
		return false
	}
	if err := c.tracker.UpdateState(ctx, id, to); err != nil {
		c.logger.Warn("dispatcher: source hold move %s %q → %q: %v", identifier, from, to, err)
		return false
	}
	return true
}
