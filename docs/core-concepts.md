# Core concepts

**Status:** describes `main`; terms checked against the DSL guide, `pkg/botregistry`
and the studio. Read this before the quickstart if "workflow", "bot" and
"bundle" all sound like the same thing.

## The short version

You describe a job as a **workflow**: a graph of steps, written in a `.bot`
file. A **bot** is a workflow you can launch — a `.bot` file, or a `.botz`
bundle that carries the file and everything it needs. Each step of the graph is
a **node**; the kind of node says who does the step: an LLM (`agent`, `judge`),
a router, a program (`tool`), a person (`human`) or another bot (`subbot`). The
studio, the CLI and the dispatcher all launch the same thing; a launch makes a
**run**.

| Term | One line | Where it lives |
|---|---|---|
| Workflow | The graph: nodes joined by edges, one entry point, budgets | The `workflow` block inside a `.bot` file |
| Bot | A launchable workflow, with a name | A `.bot` file or a bundle |
| `.bot` | Source text of a bot | Your project |
| Bundle / `.botz` | A bot packed with its skills, prompts and manifest | A `.botz` file, or an unpacked folder with a `manifest.yaml` |
| Node | One step | A declaration in the `.bot` file |
| Catalog | The list of bots the studio and dispatcher can offer | Built from your project plus what ships in the binary |
| Run | One execution of a bot | The project's store |
| Card | A task on the board that can be handed to a bot | The project's store |

## Workflow and bot

A **workflow** is the control flow: which node runs first (`entry:`), which
node follows which (edges, with conditions, loops and budgets). It is the
`workflow name:` block at the bottom of a file; the nodes it wires are
declared above it. See the [DSL guide](dsl.md#workflows-and-edges).

A **bot** is the unit you run, name and share. In day-to-day use a bot *is* its
workflow plus the prompts, schemas and resources that make it work. The two
words are used for the same object at different levels: the language says
"workflow", the CLI, the studio's **Bots** page, the board and the API say
"bot". The cloud docs put it exactly: an *agent workflow* packaged as `.bot` or
`.botz` is called a bot ([cloud overview](cloud-overview.md)). An **agent**, by
contrast, is not a bot — it is one LLM node inside one.

A minimal bot, to anchor the words (a sketch of the shape, not a complete
file — the [quickstart](quickstart.md) and the
[DSL guide](dsl.md) have runnable ones):

```text
agent plan:       an LLM drafts the work
judge review:     an LLM decides whether it is good enough

workflow ship_it:
  entry: plan
  plan -> review
  review -> plan   when not approved     # loop back
  review -> done   when approved
```

Two nodes and one `workflow` block — that is the whole bot. The
display name you see in the studio comes from the file's contract or the
bundle's manifest; the file name is what **Save** keeps.

## `.bot`, bundle and `.botz`

- A **`.bot` file** is a bot in one text file. It is enough when the workflow
  needs nothing next to it. A bot may also be split over several files that
  `import` each other ([a bot in several files](dsl.md#import--a-bot-in-several-files)).
- A **bundle** is a bot with its neighbours: a folder with `main.bot` plus an
  optional `manifest.yaml` (name, description, version, category), `skills/`,
  `prompts/`, `lib/` and `attachments/`. The manifest is what makes a bot
  *catalogued*: it gives it a name, a description and an enabled flag.
- A **`.botz` file** is a bundle packed into one deterministic archive you can
  email, commit or publish: `iterion bundle pack bots/my-bot` makes
  `my-bot.botz`, and `iterion run my-bot.botz` runs it. Details in
  [bundles](bundles.md).

One trap: `.botz` also names a **folder**. `<project>/.botz/` is where
`iterion bots install` and the marketplace unpack bundles you pulled in, and it
is one of the three places the catalog looks. Treat what is inside as
generated; edit the source bundle, not the copy under `.botz/`.

## The node kinds

Every node has an id; edges join them. The kinds you meet first
([full reference](dsl.md)):

| Kind | Who does the step | Use it for |
|---|---|---|
| `agent` | An LLM, with tools, working in your workspace | Planning, writing code, researching — work that needs judgement |
| `judge` | An LLM, same properties as `agent`, used to *evaluate* | A verdict (usually a `verdict` schema with `approved`) that an edge branches on |
| `router` | The engine | Deciding where to go next: fan out to all branches or per item, a condition, round-robin, or an LLM choice (five modes — [routers](routers.md)) |
| `tool` | A program: a shell `command` or a `script` | Build, test, `git`, anything deterministic. Its stdout, read as a JSON object, is its output. No LLM |
| `human` | A person | A pause: the run stops, the studio shows a form, the run resumes with the answer ([human in the loop](human-in-the-loop.md)) |
| `subbot` | Another bot | A real nested run with its own loops, state and budget ([sub-bots](groups-iteration-subbots.md)) |

Also in the language, less often needed at first: `compute` (bounded
expressions, no LLM or shell), `emit`/`wait`/`await_answers` (events and
non-blocking questions), and `group`/`use` (reusable fragments).

A `tool` node runs its `command` in the host shell of the operating system:
`bash -c` on Linux and macOS, PowerShell on Windows
([shell portability](workflow_authoring_pitfalls.md#shell-portability-for-tool-nodes)).

## The bot catalog and where it comes from

The **catalog** is the one list the studio's **Bots** page, the Home page's Bots
tile and file list, the Launch picker, the board's bot picker and the Dispatcher settings
all read (`GET /api/v1/bots?include=all`). Each entry shows where it came
from — its *origin*:

| Origin | What it is | Where it comes from |
|---|---|---|
| **Project** | A bundle with a manifest | Folders `bots/`, `examples/` and `.botz/` of the project, plus any `--bots-path` you gave `iterion studio` |
| **Project file** | A single `.bot` file without a manifest | Directly in the project folder, with no walk of its subfolders: a file in `workflows/` or `docs/`, a `lib/` fragment, test data and a nested clone of another project are not bots of this project (open such a file by its path, **Open…** → *Open by path*) |
| **Team**, **Platform** | Bots of your team or of the whole instance | Cloud only |

The `iterion` binary carries no bots of its own (owner decision, 2026-11-05):
the zero-config dispatcher runs the bots of your project
([dispatcher](dispatcher.md#quick-start-zero-config)). A bundle and
a loose file already covered by it are listed once. *Enabled* / *Disabled* is the manifest's catalog flag: a
disabled bot is not offered by routing, the board picker or the assistant but
can still be launched by hand. A project file has no
manifest, so it is always enabled; it has no bot page — open it
in the editor. The list shown to routing and integrations without the
`?include=all` is the narrower catalog-only one. See [visual editor](visual-editor.md)
for the screens and [bundles](bundles.md) for the manifest.

## What is in your project and what is in the store

| In your project | In the store (outside it, by default) |
|---|---|
| `.bot` files and bundles — your source | Runs and their events and artifacts |
| `.botz/` unpacked bundles | The board: cards, columns, history |
| `.iterion/authoring/` folders beside saved files — the studio's save locks and recovery copies | The dispatcher's settings and the bots extracted for it |
| Branches your runs created, when a bot uses `worktree: auto` | The per-run checkouts (`worktrees/`) |

Your project is source; the store is state. You can move the project without
losing anything it needs to run, and wipe the store without touching a line of
your bots. The default location of the store, the rule that picks it and how to
override it are in [Where iterion keeps data](where-iterion-keeps-data.md).

## Around the bot

- A **run** is one execution: events, per-node outputs, cost, a checkpoint to
  resume from. Runs are listed on the studio's **Runs** page.
- A **board** is a kanban of **cards** (issues). A card can name a bot — its
  *assignee* or *bot* field — and the **dispatcher** polls the board and runs
  that bot for each ready card. A card with neither — or whose assignee is not a
  bot of the project — is not run by the zero-config dispatcher
  ([dispatcher](dispatcher.md), [native tracker](native-tracker.md)).
- A **worktree** is a separate git checkout a run can work in (`worktree:
  auto`), so your own working tree is never edited; the result lands as a branch
  you merge ([merge policy](merge-policy.md)).
- The **studio** is the web UI (`iterion studio`, or inside the desktop app):
  [Studio overview](studio-overview.md).
