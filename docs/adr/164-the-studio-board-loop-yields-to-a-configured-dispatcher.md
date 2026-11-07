# ADR-164 — The studio board loop yields to a configured dispatcher, and a board launch never runs in place

- **Status**: Accepted
- **Date**: 2026-11-06
- **Amends**: [ADR-074](074-dedicated-pipeline-board-projection.md) — D5 (the default cap) and D6 (the gate of the launch loop)

## Context

Two live incidents on 2026-11-06 (project `itterion-test`, card "after Stop of the dispatcher the studio launches the card by itself"):

- After the operator pressed **Stop** (or **Pause**) on the dispatcher, the studio's pipeline admission loop launched the Ready cards at once: the gate of D6 was `Dispatcher.Status().State == running`, and a stopped or paused manager is not running although its config is kept. On Pause, 33 runs started together.
- Nothing bounded the loop on the desktop: only `iterion studio` defaulted `--max-concurrent-pipelines` to 3; the desktop app and `iterion server` passed 0, which means "use `ITERION_MAX_CONCURRENT_PIPELINES`", unset = unlimited.
- The launches ran in place in the project folder (a non-git folder degrades `worktree: auto` to in-place), were labelled "Manual", and a cancelled run hung in `running` until a full-folder workspace scan finished, because the `fail:` boundary capture ignored the cancelled context and ran before the status write.

## Decision

1. **A saved dispatcher config means the dispatcher owns the board.** The loop stands down when `Status().HasConfig || State != idle`. Stop means "nothing is launched from the board". A project with no dispatcher config keeps the loop, and the explicit "Launch now" is never gated.
2. **The default cap is 3 on every local entry point** (`cli.DefaultMaxConcurrentPipelines`: `iterion studio`, `iterion server` local mode, the desktop app). `0` stays "unlimited" for an explicit flag; `ITERION_MAX_CONCURRENT_PIPELINES` is unchanged.
3. **A board launch requires a worktree.** `LaunchSpec.RequireWorktree` → `runtime.WithRequireWorktree()`, set only by the board lane. A non-git folder or a repository with no commits fails the run at setup with a message naming `git init` / `worktree: none`. The engine contract for `iterion run`, e2e and examples (auto is best-effort isolation) is unchanged.
4. **A board launch has its own source kind**, `pipeline_board` (`store.RunSourceKindPipelineBoard`), shown as "Pipeline board" in the Runs list; the run-launched audit trigger carries it.
5. **The stop-boundary capture is cancellable and bounded.** `Tracker.CaptureContext` aborts with its context; the `fail:`/`pause:` capture taken when a node stops runs under a detached context with a 10-second budget. Losing that boundary costs rewind fidelity, never the cancel.

## Consequences

- An operator with a saved dispatcher config who relied on the studio loop alone sees Ready cards stand still until the dispatcher is started or "Launch now" is used.
- A board launch of a bot that relies on the in-place degrade (a greenfield bot in an empty non-git folder) now fails fast; declare `worktree: none` or `git init`.
- Older studio builds render the `pipeline_board` kind as Manual (the field is free-form).
- The flag is in-process only: queued runs recovered after a restart and the detached/cloud paths do not carry it.

## Rejected alternatives

- Widen the gate to `State != idle` only: still starts the board after Stop.
- A new "admission mode" setting: a feature; a saved config already is the operator's statement.
- Refuse the in-place degrade in the runtime for everyone: breaks scratch-dir runs and greenfield bots.
- Reuse the `dispatcher` source kind: would relabel studio runs and trigger dispatcher-only readers.
- Drop the stop-boundary capture, or run it after the status write: loses rewind fidelity, or races a rewind claiming the run.

## Where applied

`pkg/server/pipeline_admission.go` (`dispatcherOwnsBoard`, `launchTicketNow`), `pkg/store/run.go`, `pkg/runview/service.go` and `service_launch.go`, `pkg/runtime/engine_run.go` and `engine_options.go`, `pkg/runtime/engine_exec.go` (`captureStopBoundary`), `pkg/workspacetrack/native.go` and `tracker.go`, `pkg/cli/studio.go`, `cmd/iterion*`, `studio/src/components/Runs/runSourceMeta.ts`. See [native tracker](../native-tracker.md) and [workspace versioning](../workspace-versioning.md).
