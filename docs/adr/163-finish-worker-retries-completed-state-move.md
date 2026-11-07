# ADR-163 — The finish worker retries the completed-state move; an unfinished transition is completed, not re-run

- **Status**: Accepted — design by the architect for the bug "the dispatcher re-runs a finished card when the move to `completed_state` did not land" (card `native:45835db4`, live case of 2026-11-06).
- **Date**: 2026-11-06
- **Code context**: [`pkg/dispatcher/commands.go`](../../pkg/dispatcher/commands.go) (`maybeTransitionToCompleted`, `completeWithRetry`, `runFinishWorker`), [`pkg/dispatcher/loop.go`](../../pkg/dispatcher/loop.go) (`finishedTransitionPending`, `completeFinishedCard`, `dispatch`), [`pkg/dispatcher/tracker/tracker.go`](../../pkg/dispatcher/tracker/tracker.go) (`Issue.StateAt`).
- **Relates to**: [ADR-028 dispatcher actor I/O offload](028-dispatcher-actor-io-offload.md) (Step 3: transition first, Release last), [ADR-015 retry attempt cap](015-dispatcher-retry-attempt-cap.md) (the retry queue and `max_attempts` this decision deliberately stays out of).

## Context

A clean run ends in the finish worker, which moves the card from `running_state` to `completed_state` and then releases the claim. The move's error was only logged. `running_state` is an eligible column (so a crash can be recovered), so a card whose move failed — a transient store or tracker error; on Windows a lost `rename` under load — was re-picked at the next tick and `resolveRunID` minted a fresh run: a second full paid run of a task that was already done. The claim watchdog could not help (the claim had been released normally), and `lastRunForbidsFresh` deliberately does not include `finished`, because dragging a finished card back to an eligible column is the operator's re-queue gesture.

Two things were missing: a failed move was never retried, and dispatch had no durable way to tell "the run finished but the move was not written" from "the operator re-queued a finished card".

## Decision

1. **Retry inside the finish worker, while it holds the claim.** `completeWithRetry` repeats `maybeTransitionToCompleted` with the retry queue's capped backoff (`min(10s·2^(n-1), agent.max_retry_backoff_ms)`), without an attempt limit and without a new knob. `maybeTransitionToCompleted` now returns an error only for transient failures; a missing state, a refused move, an already-moved card, a vanished card and a superseded claim (`ErrClaimConflict`) end the loop. The loop also ends when the dispatcher stops. Release stays last (ADR-028 Step 3). While the move fails, the worker posts a `completion_pending` dispatch skip so the Dispatcher page and the card say why the card is still in `running_state`; it posts the clearing command when the move finally lands.
2. **A durable dispatch-time guard.** A card in `running_state` whose last run is `finished` and which entered that column **before** the run finished (`tracker.Issue.StateAt` earlier than `Run.FinishedAt`) is an unfinished transition: dispatch completes the move on the claim it just took (no run, no slot) instead of minting a run. A card moved into `running_state` after the run finished is a re-queue and still runs. A zero `StateAt` (legacy card) counts as unfinished. This covers a restart or a kill in the middle of the retry, so the in-memory loop is not load-bearing for correctness.

`tracker.Issue.StateAt` is the native store's existing column-entry stamp (both twins), exposed on the tracker value; no new durable field.

## Consequences

- A finished card is never re-run because of a lost move; the retry is visible on the card and the Dispatcher page.
- The re-queue contract of `lastRunForbidsFresh` (and its registry test) is unchanged: the guard is a separate predicate evaluated before it.
- The cloud dispatcher is not changed: no durable state is added, so no twin is owed; it keeps its own watchdog filing of finished runs.
- A board that never defines `completed_state` (the move is refused, benign) leaves the card in `running_state`; the guard then claims and releases it on every poll without starting a run. That churn is cheap compared with the paid re-run it replaces; `completed_state: none` is the documented opt-out.
- "Same bot" from the card text is approximated by the `StateAt` against `FinishedAt` comparison: a run record carries no field comparable to the card's `bot`, and a bot change is in practice accompanied by a re-queue move.

## Rejected alternatives

- **Reuse the retry queue (`scheduleRetry`, `cmdRetryDue`).** That queue means "run the bot again": attempts, a previous run id, a give-up to `failed_state`. A transition retry is not a run; counting a clean run toward `max_attempts` could give up a finished card into `blocked`.
- **A durable marker on the card (a new native and Mongo field).** A new storage format with its cloud twin, written by the very store that is failing; `StateAt` already carries the needed fact.
- **Make `finished` forbid a fresh run in `running_state` unconditionally.** It breaks the accepted re-queue gesture; the `StateAt` against `FinishedAt` comparison removes the conflict.

## Where applied

`pkg/dispatcher/commands.go`, `pkg/dispatcher/loop.go`, `pkg/dispatcher/state.go` (`DispatchSkipCompletionPending`), `pkg/dispatcher/tracker/tracker.go`, `pkg/dispatcher/native/adapter.go`; behaviour described in [dispatcher](../dispatcher.md#terminal-state-keys). Tests: `pkg/dispatcher/completed_transition_retry_test.go`.
