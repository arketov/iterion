# ADR-169 — The ticket router outranks `require_assignee` on a tracker without a PR probe

- **Status**: Accepted — the owner's answer (variant A) on the board card "ticket_router sends an unassigned card to the paid feature-dev" (2026-11-12).
- **Date**: 2026-11-12

## Context

`ticket_router` is an opt-in (`ticket_router.enabled`). For a card with neither bot nor assignee it asks the tracker whether a pull request already links the card: a linked PR makes the dispatcher step aside, none routes the card to the implement bot (`feature-dev` by default). The probe (`HasLinkedPR`) is an optional tracker capability; only the GitHub adapter has it. On native and forgejo `routeUnassignedIssue` degrades to the implement bot.

The router runs in `dispatch` before `refuseWithoutBot`, so it names a bot first and `require_assignee` sees `Bot != ""` and does not refuse. An operator who enables the router on a native board therefore gets every unassigned card in `ready` started on a paid `feature-dev` run, silently — the card read as a bug: "the PR check does not work and degrades".

## Decision

1. **The behaviour stays.** The router is an explicit opt-in and outranks `require_assignee`; a tracker that cannot probe linked PRs routes every unassigned card to the implement bot. Making the router a no-op there would turn the documented opt-in off on native and forgejo.
2. **It is no longer silent.** `routeUnassignedIssue` logs one WARN per process (`state.routerNoProbeWarned`, actor-owned, no mutex) the first time it routes on a tracker without the probe, naming the implement bot and the precedence.
3. **It is documented.** `docs/dispatcher.md` (ticket router section) and the board-dispatcher diagram state the precedence and the remedy: keep `ticket_router` off so unassigned cards wait for an assignee.

## Consequences

- No CLI flag, DSL, config key, file format or API shape changes. The zero-config `iterion dispatch` / desktop preset does not enable the router and keeps `require_assignee: true`, so the default path still holds unassigned cards.
- The operator who turns the router on for native still pays for unassigned cards, but is told once. A config reload does not repeat the WARN.
- `TestRouteUnassignedIssue_NoProbeWarnsOnce` pins the single WARN; `TestDispatchRouterOutranksRequireAssignee` pins the precedence — changing it changes the test and the docs together.

## Rejected

- **The router defers to `require_assignee` (a no-op on trackers without a probe).** It keeps unassigned cards waiting, but removes the router from native and forgejo, where it is the only way to auto-route an unassigned card.
- **A WARN per card.** The refusal path repeats on every poll; one line per process is enough to tell the operator.

## Where applied

`pkg/dispatcher/loop.go` (`dispatch`, `routeUnassignedIssue`), `pkg/dispatcher/state.go` (`routerNoProbeWarned`), `pkg/dispatcher/config.go` (`TicketRouterConfig`), `pkg/dispatcher/ticket_router_test.go`; described in [dispatcher](../dispatcher.md).
