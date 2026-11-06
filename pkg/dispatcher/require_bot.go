package dispatcher

import (
	"fmt"

	"github.com/arketov/iterion/pkg/botregistry"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// refuseWithoutBot reports whether iss must not be dispatched because, with
// Config.RequireAssignee on, it names no bot the dispatcher can run — and
// records the matching dispatch skip so the board and the Dispatcher page show
// why the card is idle. The owner's rule: a card without a bot executor does
// not start, and the paid default workflow is never chosen for it.
//
// Two shapes are refused:
//   - no assignee and no bot at all (DispatchSkipNoAssignee);
//   - an assignee that is not a bot of this dispatcher (DispatchSkipNoBot): a
//     person's name, a typo, or a bot switched off in the catalogue. Routing
//     would otherwise send it to the default workflow and run it silently.
//
// An explicit iss.Bot is left to resolveExplicitBot, which refuses what it
// cannot resolve. With RequireAssignee off nothing is refused here: the
// documented fallback to the default `workflow:` is unchanged for hand-written
// configs. A runner that cannot answer HasRoute (a plain single-workflow
// runner) has no routing concept, so an assignee is not judged against it.
//
// Runs on the actor goroutine, like the rest of dispatch().
func (c *Dispatcher) refuseWithoutBot(cfg *Config, iss tracker.Issue) bool {
	// A dispatcher built with no fallback workflow has nothing to run a card no
	// bot routes to, whatever the setting says now: a live config reload does
	// not rebuild the runner, so require_assignee switched off after the start
	// would otherwise claim such a card and fail it on every retry.
	if rr, ok := c.runner.(*RoutingRunner); ok && rr.Default == nil && iss.Bot == "" {
		return c.refuseUnroutable(cfg, iss)
	}
	if !cfg.RequireAssignee || iss.Bot != "" {
		return false
	}
	return c.refuseUnroutable(cfg, iss)
}

// refuseUnroutable is the body of refuseWithoutBot once the policy applies.
func (c *Dispatcher) refuseUnroutable(cfg *Config, iss tracker.Issue) bool {
	// A card with a pending retry already started under an earlier policy (e.g.
	// before an upgrade): let its retry/resume run its course, when there is a
	// default workflow to run it on.
	hasDefault := cfg.Workflow != ""
	if rr, ok := c.runner.(*RoutingRunner); ok && rr.Default == nil {
		hasDefault = false // the runner was built without one
	}
	if _, retrying := c.state.retries[iss.ID]; retrying && hasDefault {
		return false
	}
	if iss.Assignee == "" {
		c.recordDispatchSkipKind(iss, DispatchSkipNoAssignee,
			"no assignee: assign a bot to this card to run it (the dispatcher does not fall back to the default bot)")
		return true
	}
	rc, ok := c.runner.(interface{ HasRoute(string) bool })
	if !ok || rc.HasRoute(iss.Assignee) {
		return false
	}
	reason := fmt.Sprintf("assignee %q is not a bot of this project: assign one of the project's bots (bots/<name>/) — the dispatcher does not fall back to a default bot", iss.Assignee)
	if _, err := botregistry.ResolveBotEntry(iss.Assignee, cfg.Bots.Paths); err == nil {
		reason = fmt.Sprintf("bot %q is disabled in the catalogue: enable it on the Bots page to run this card", iss.Assignee)
	}
	// Said once per reason: the same card is refused again on every poll.
	if prev, seen := c.state.dispatchSkips[iss.ID]; !seen || prev.Reason != reason {
		c.logger.Warn("dispatcher: %s: %s — not running it", iss.Identifier, reason)
	}
	c.recordDispatchSkipKind(iss, DispatchSkipNoBot, reason)
	return true
}
