# ADR-166 — Settled-claim sweep

- **Status**: Accepted — the gateway's design for the board card "a claim is not released when a run resumed after a pause moved its own card to a terminal column" (2026-11-06).
- **Date**: 2026-11-09
- **Relates to**: [ADR-014](014-dispatcher-paused-run-parking.md) (a paused run keeps its claim, which this decision leaves as it is), [ADR-096](096-board-claim-lease-and-watchdog.md) (the fence and the "transition first, release last" order)

## Context

A run that pauses on a human node leaves its card claimed (ADR-014): the claim is what keeps `ListCandidates` away. `reconcileParked` releases that claim, but only for cards still in `awaiting_input`. When the run is answered out of band and moves its OWN card (to `blocked`, say) the card leaves `awaiting_input`, and when an operator then drags it back to `ready` the claim, `claimed_at` and `claim_lease_until` are still on it. `ListCandidates` drops claimed cards, so the dispatcher never takes the card again. The only other exits are the boot sweep (a dead pid) and the claim watchdog, which is off by default.

## Decision

1. **A lease-gated sweep, `reconcileSettledClaims`, runs on every tick after `reconcileParked`.** It releases a claim held by THIS daemon's marker when its lease ran out, the card is in no worker or retry slot and is not in `awaiting_input`, and the last run is settled: no `last_run`, `finished` or hard `failed`. The column is not looked at. The card is never moved.
2. **Paused, `failed_resumable`, `cancelled`, running, queued and unreadable runs keep the claim.** A zero lease (a legacy claim) is skipped, as the watchdog does.
3. **"Retry from zero" (`POST …/reset` with `{"fresh": true}`) releases the claim itself**, after the pointer is dropped and the card restaged. Plain Retry and plain reset do not: the server cannot tell a dead hold from a live worker's claim, so they wait for the sweep (at most one lease, 15 minutes after the last heartbeat).

## Consequences

- A card the dispatcher stopped taking returns to the pool by the ordinary rule: a `finished` or hard-failed last run allows a fresh run when the card sits in an eligible column.
- The wait is bounded by the lease; before, it was unbounded.
- "Retry from zero" releases without checking for a live worker of the dispatcher; it already refuses while any run in the tree is non-terminal, and the button is the dangerous one.
- No store, board, Mongo or config change. The cloud twin parks a card without a claim, so it has nothing to sweep.

## Rejected

- **Clearing the claim inside `SetState` or `Reopen`**: breaks "transition first, release last" and would make a live worker's heartbeat get `ErrClaimConflict` and cancel a run that moved its own card; it also touches both store twins and their conformance tests.
- **Releasing without the lease gate**: `finishRun` drops the running entry before the finish worker's last writes (the transition, the give-up stamp, the release); a release in that window re-dispatches an `in_progress` card or loses the stamp.
- **Releasing in the plain Retry handler**: the server has no view of the dispatcher's running set, and the local `last_run` is written only when a run ends, so a live new run looks like a dead old one.
