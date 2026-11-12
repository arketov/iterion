package dispatcher

import (
	"fmt"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// noteQueued keeps the dispatch-skip entry of a candidate that has no free slot
// truthful. dispatch() is never reached for such a card, so without this a
// refusal recorded before the operator (or a board trigger) assigned a bot
// stays on the card for as long as the slots are busy. Actor goroutine only.
func (c *Dispatcher) noteQueued(cfg *Config, iss tracker.Issue) {
	if prev, ok := c.state.dispatchSkips[iss.ID]; ok {
		switch prev.Kind {
		case DispatchSkipNoAssignee, DispatchSkipNoBot, DispatchSkipQueued:
		default:
			return // another refusal (source_changed, completion_pending, last-run hold, bot misconfiguration) keeps its own text
		}
	}
	// The ticket router assigns a bot inside dispatch(); judging such a card
	// here would label it no_assignee although it is about to be routed.
	if cfg.TicketRouter.Enabled && iss.Bot == "" && iss.Assignee == "" {
		return
	}
	if c.refuseWithoutBot(cfg, iss) {
		return // records no_assignee / no_bot with the card's current bot and assignee
	}
	c.recordDispatchSkipKind(iss, DispatchSkipQueued, c.queuedReason(cfg, iss.WorkflowState))
}

// queuedReason is the operator-facing text of the queued skip: it names the cap
// that holds the card back.
func (c *Dispatcher) queuedReason(cfg *Config, state string) string {
	if limit, ok := cfg.Agent.MaxConcurrentByState[state]; ok && c.state.slotsByState[state] >= limit {
		return fmt.Sprintf("queued — waiting for a slot (%s %d/%d)", state, c.state.slotsByState[state], limit)
	}
	return fmt.Sprintf("queued — waiting for a slot (running %d/%d)", len(c.state.running), cfg.Agent.MaxConcurrent)
}
