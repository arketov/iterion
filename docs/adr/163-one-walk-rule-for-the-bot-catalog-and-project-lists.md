# ADR-163 — One walk rule for the bot catalog and the project's lists

- **Status**: Accepted — the gateway's design for the board card "the bot catalog and `/api/files` diverge at the edges" (2026-11-06); the owner's direction: the lists hold only the user's workflows, no `testdata`, no foreign clones.
- **Date**: 2026-11-06
- **Relates to**: [ADR-140](140-discovery-claims-what-the-bundle-package-claims.md) (the bundle claim and the unopenable report, which this rule leaves as they are)

## Context

Two walks read the discovery roots (`bots/`, `examples/`, `.botz/`, `--bots-path`): the catalog's `discoverBots` and the project list's `ListProjectWorkflows` behind `GET /api/files`. They pruned differently. The list skipped hidden directories, `node_modules` and nested git checkouts; the catalog skipped only scaffold staging directories, a bot's `lib/` fragments and the inside of a bundle. So `bots/clone/{a.bot,.git}`, `bots/.x/h.bot` and the twelve parser fixtures under `bots/testdata/` were routable bots — the dispatcher, `iterion run` and the studio could route to a fixture — while the editor never listed them. Neither walk skipped `testdata`.

## Decision

1. **One pruning rule, implemented once in `pkg/botregistry` and applied by both walks.** Below a root a walk never enters a directory named by `walkSkippedDir` (a hidden directory — a scaffold's staging directory among them — `node_modules`, or `testdata`, exact lowercase match), nor a nested git checkout (`isNestedRepo`: a directory holding a `.git` entry, file or directory). The order is the same in both: pruned name, then a `lib/` fragment directory, then the bundle claim, then the nested-repo check.
2. **A bundle that is a git clone is still a bundle.** The claim precedes the nested-repo check (ADR-140), in both walks.
3. **A walk root is never pruned.** Naming a directory as a root (`--bots-path bots/testdata`, `bots.paths` in the dispatcher config) still discovers what is in it; a `.bot` file named as a root is routable. A directory that must be routable despite a `.git` becomes a bundle (give it a manifest).
4. **A broken manifest is the one difference, on purpose.** The catalog answers "what is routable": a bundle whose manifest does not load is not routable and lands in `discovery_errors`. The list answers "what the editor can open": the same bundle stays a row, because the editor is where it is repaired. Parsing manifests in the list would cost a read per bundle and hide the broken bundle from the editor. `TestCatalogAndProjectListDifferOnABrokenManifestByDesign` pins it; changing it changes the test and the docs together.

## Consequences

- **A documented behaviour break.** The routable set shrinks: nothing under a hidden directory, `node_modules`, `testdata` or a nested git checkout that is not itself a bundle is listed, routed or reported in `discovery_errors` any more. A user whose loose `.bot` files live in a nested checkout or submodule under `bots/` loses routing for them; the hatches are a manifest or a named root.
- `--bots-path .` stops walking `.git`, `.iterion`, `.claude/worktrees`, `node_modules` and nested clones.
- `iterion bots create`'s name-collision check goes through discovery, so it no longer sees a name that lives only under a pruned directory.
- The twelve `bots/testdata/*` fixtures leave `routableNonWorkflows` in `bots/catalog_parse_compile_test.go`; its discovery floor drops from 60 to 50.
- `TestCatalogAndProjectListWalkTheSameTree` holds the two walks to one tree, so drift is a red test.
- No CLI flag, DSL, file format, config key or response shape changes.

## Rejected

- **Building the list on `discoverBots`**: the list would need entries mapped back to project-relative rows, root-file handling, the `Skipped` shape and the cap ordering — a refactor of a second consumer for no gain.
- **A shared `walkRoot` driver**: cleaner, but the walks differ in what they do per node; the equivalence test is the cheaper guard.
- **Reusing `workflowfile.SkipWalkDir`**: it serves `iterion fmt` and the corpus guards, keeps `testdata` walkable on purpose, and includes `vendor`.

## Where applied

`pkg/botregistry/projectfiles.go` (`walkSkippedDir`, `isNestedRepo`), `pkg/botregistry/registry.go` (`discoverBots`), `pkg/botregistry/projectfiles_test.go`, `bots/catalog_parse_compile_test.go`; described in [core-concepts](../core-concepts.md) and [environment-variables](../environment-variables.md).
