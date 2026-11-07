package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// The operator's two gestures on a row of the retry queue: run it now instead
// of waiting out the backoff, or stop retrying it. Both are commands to the
// actor, so they serialise with the timer, the tick and a dispatch consuming
// the entry.

// Typed refusals of GiveUpRetry; the HTTP layer maps them to statuses.
var (
	// ErrRetryNotQueued: no retry entry for the issue (never queued, already
	// dispatched, or removed by someone else a moment ago).
	ErrRetryNotQueued = errors.New("dispatcher: the issue has no pending retry")
	// ErrRetryNoFailedState: agent.failed_state is empty, so there is no
	// state to file the card under; dropping only the queue entry would let
	// the next tick relaunch the card from attempt 0, paid.
	ErrRetryNoFailedState = errors.New("dispatcher: agent.failed_state is not set, so there is no state to give the card up into")
)

// retryNowReply is what cmdRetryNow answers.
type retryNowReply struct {
	found  bool
	paused bool
}

// cmdRetryNow makes a pending retry due immediately: the backoff timer is
// stopped, the entry is marked fired (the card is eligible again) with its
// attempt count and resume pointer intact, and a poll tick is requested.
type cmdRetryNow struct {
	issueID string
	reply   chan<- retryNowReply
}

func (m cmdRetryNow) apply(c *Dispatcher, _ context.Context) {
	cur, ok := c.state.retries[m.issueID]
	if !ok || cur.Removing {
		m.reply <- retryNowReply{}
		return
	}
	if cur.Timer != nil {
		cur.Timer.Stop()
	}
	cur.Fired = true
	cur.DueAt = time.Now()
	c.logger.Info("dispatcher: %s retry forced by the operator (attempt %d kept)", cur.Identifier, cur.Attempt)
	m.reply <- retryNowReply{found: true, paused: c.paused.Load()}
	c.fireSnapshot()
	c.Refresh()
}

// holdRetryReply is what cmdHoldRetry answers: the facts GiveUpRetry needs,
// read on the actor.
type holdRetryReply struct {
	found       bool
	identifier  string
	attempt     int
	prevRunID   string
	failedState string
}

// cmdHoldRetry freezes a pending retry for removal: the timer stops and the
// entry stays unfired, so the card stays held (isClaimed) and no tick or timer
// can relaunch it while the give-up is being written.
type cmdHoldRetry struct {
	issueID string
	reply   chan<- holdRetryReply
}

func (m cmdHoldRetry) apply(c *Dispatcher, _ context.Context) {
	cur, ok := c.state.retries[m.issueID]
	if !ok || cur.Removing {
		m.reply <- holdRetryReply{}
		return
	}
	if cur.Timer != nil {
		cur.Timer.Stop()
	}
	cur.Removing = true
	cur.Fired = false
	m.reply <- holdRetryReply{
		found:       true,
		identifier:  cur.Identifier,
		attempt:     cur.Attempt,
		prevRunID:   cur.PrevRunID,
		failedState: c.cfg.Load().Agent.FailedState,
	}
	c.fireSnapshot()
}

// cmdReleaseRetryHold undoes cmdHoldRetry when the give-up could not be
// written: the entry goes back to waiting (or due, when its time has passed).
type cmdReleaseRetryHold struct {
	issueID string
}

func (m cmdReleaseRetryHold) apply(c *Dispatcher, _ context.Context) {
	cur, ok := c.state.retries[m.issueID]
	if !ok || !cur.Removing {
		return
	}
	cur.Removing = false
	if delay := time.Until(cur.DueAt); delay > 0 {
		issueID := m.issueID
		cur.Timer = time.AfterFunc(delay, func() {
			select {
			case c.cmds <- cmdRetryDue{issueID: issueID}:
			case <-c.stop:
			}
		})
	} else {
		cur.Fired = true
	}
	c.fireSnapshot()
}

// RetryNow makes the pending retry of issueID due now, keeping its attempt
// count and its resume pointer. found is false when the issue has no pending
// retry; paused reports that dispatch is paused, so the retry runs after
// Resume. Blocks until the actor answers; false after Stop. It returns
// ctx.Err() when the caller gave up first: a command already enqueued still
// applies, which is harmless — it is idempotent.
func (c *Dispatcher) RetryNow(ctx context.Context, issueID string) (found, paused bool, err error) {
	// A select with two ready cases picks one at random: a cancelled request
	// must not enqueue anything.
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	reply := make(chan retryNowReply, 1)
	select {
	case c.cmds <- cmdRetryNow{issueID: issueID, reply: reply}:
	case <-ctx.Done():
		return false, false, ctx.Err()
	case <-c.stop:
		return false, false, nil
	}
	select {
	case r := <-reply:
		return r.found, r.paused, nil
	case <-ctx.Done():
		return false, false, ctx.Err()
	case <-c.stop:
		return false, false, nil
	}
}

// GiveUpRetry stops retrying issueID: the card is moved to agent.failed_state
// (default "blocked") and stamped as given up by the dispatcher, exactly as
// when its attempt budget runs out, and its retry entry is removed. Dropping
// the entry alone would hand the card to the next tick as a fresh paid launch
// from attempt 0; the filed state is what makes the removal stick, and
// reopening the card on the board is the way back. state is the state the
// card was filed under. Errors: ErrRetryNotQueued, ErrRetryNoFailedState, or
// the tracker's refusal of the move (the entry is then left as it was), or
// ctx.Err() when the caller gave up while waiting for the actor.
func (c *Dispatcher) GiveUpRetry(ctx context.Context, issueID string) (state string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reply := make(chan holdRetryReply, 1)
	select {
	case c.cmds <- cmdHoldRetry{issueID: issueID, reply: reply}:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.stop:
		return "", ErrRetryNotQueued
	}
	var held holdRetryReply
	select {
	case held = <-reply:
	case <-ctx.Done():
		// The hold is queued and will apply. Lift it once the actor answers —
		// only when it did find the entry: a release after !found would lift
		// another request's hold. The release must not depend on ctx.
		go func() {
			select {
			case h := <-reply:
				if h.found {
					select {
					case c.cmds <- cmdReleaseRetryHold{issueID: issueID}:
					case <-c.stop:
					}
				}
			case <-c.stop:
			}
		}()
		return "", ctx.Err()
	case <-c.stop:
		return "", ErrRetryNotQueued
	}
	if !held.found {
		return "", ErrRetryNotQueued
	}
	// Every way out but the filed one lifts the hold — a refusal, an error and
	// a panic in the tracker alike — or the entry would stay held, unfired and
	// without a timer, until the studio restarts.
	filed := false
	defer func() {
		if filed {
			return
		}
		select {
		case c.cmds <- cmdReleaseRetryHold{issueID: issueID}:
		case <-c.stop:
		}
	}()
	if held.failedState == "" {
		return "", ErrRetryNoFailedState
	}
	mvCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.fencedUpdateState(mvCtx, issueID, held.failedState, nil); err != nil {
		return "", fmt.Errorf("move %s to %q: %w", held.identifier, held.failedState, err)
	}
	filed = true
	if setter, ok := c.tracker.(giveUpStamper); ok {
		stamp := &native.GiveUp{
			RunID:    held.prevRunID,
			State:    held.failedState,
			Attempts: held.attempt,
			Reason:   "removed from the retry queue by the operator",
			Launch:   held.prevRunID == "",
			At:       time.Now().UTC(),
		}
		if werr := setter.SetGaveUp(issueID, stamp); werr != nil {
			c.logger.Warn("dispatcher: stamp give-up on %s: %v", held.identifier, werr)
		}
	}
	c.logger.Warn("dispatcher: %s removed from the retry queue by the operator after %d attempt(s) — moved to %q; reopen the card to try again",
		held.identifier, held.attempt, held.failedState)
	c.postCmd(cmdDropRetry{issueID: issueID})
	return held.failedState, nil
}

// serveRetryNow is POST .../retries/{id}/run-now: 202 {"issue_id","status":
// "due","paused"}; 404 when the issue has no pending retry; 503 when the
// request was cancelled before the dispatcher answered.
func (c *Dispatcher) serveRetryNow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, paused, err := c.RetryNow(r.Context(), id)
	if err != nil {
		WriteErr(w, http.StatusServiceUnavailable, err)
		return
	}
	if !found {
		WriteErr(w, http.StatusNotFound, ErrRetryNotQueued)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"issue_id": id, "status": "due", "paused": paused})
}

// serveRetryDrop is DELETE .../retries/{id}: 200 {"issue_id","status":
// "given_up","state"}; 404 when the issue has no pending retry; 409 when there
// is no state to file the card under or the board refuses the move; 502 when
// the tracker fails; 503 when the request was cancelled or timed out before
// the dispatcher answered.
func (c *Dispatcher) serveRetryDrop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	state, err := c.GiveUpRetry(r.Context(), id)
	switch {
	case err == nil:
		WriteJSON(w, http.StatusOK, map[string]string{"issue_id": id, "status": "given_up", "state": state})
	case errors.Is(err, ErrRetryNotQueued):
		WriteErr(w, http.StatusNotFound, err)
	case errors.Is(err, ErrRetryNoFailedState):
		WriteErr(w, http.StatusConflict, fmt.Errorf("%w: move the card out of the dispatch column on the board instead", err))
	case errors.Is(err, tracker.ErrTransitionRejected), errors.Is(err, tracker.ErrNotSupported):
		WriteErr(w, http.StatusConflict, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		WriteErr(w, http.StatusServiceUnavailable, err)
	default:
		WriteErr(w, http.StatusBadGateway, err)
	}
}
