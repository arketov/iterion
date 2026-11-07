# Studio overview

**Status:** describes the local studio (`iterion studio`, and the same UI inside
the [desktop app](desktop.md)) on `main`; checked against `studio/src`
(`NavLinks.tsx`, `navAvailability.ts`, the Board, Dispatcher and launch views)
and the server's `server_info` flags. The cloud shell has a smaller sidebar and
is not described here. Screenshots and every editor feature are in the
[visual editor](visual-editor.md) page; the vocabulary is in
[Core concepts](core-concepts.md).

## One studio, one project

A studio server serves **one project folder**. The sidebar shows its name,
read-only, above the command launcher (`Ctrl+K`). Everything you see — the
editor's files, the Bots catalog, the Board, Runs, the Dispatcher — belongs to
that project, and its runs and cards are kept in the project's store
([where](where-iterion-keeps-data.md)).

- **Browser studio** (`iterion studio --dir <project>`): one server, one
  project. There is no project switcher; for another project start another
  studio and open it in its own tab or window. (`iterion studio --workspace`
  is the exception: one process hosts every registered project, each under its
  own `/x/<project-id>/` address — see the [CLI reference](cli-reference.md#iterion-studio).)
  A single-project server never changes its folder at run time: it answers
  `POST /api/projects/switch`, or `POST /api/projects` for another folder, and
  `DELETE /api/projects/{id}` of its own folder with `409`
  `project_switch_unsupported`, and leaves the registry untouched.
- **Desktop app:** pick the project in the welcome wizard. `Ctrl+P` (or the
  native menu) switches projects: the window reattaches to that project's own
  server and reloads, so every page follows.

## The sidebar

A page appears only when the server supports it, so the list you see can be
shorter than this one. **Sitemap**, at the bottom of the sidebar, lists exactly
the pages *your* server answers.

| Group | Page | What it is for |
|---|---|---|
| — | **Home** | The start page: a *What's Next* entry, tiles for the other pages your server has, your project's `.bot` files and recent runs. |
| — | **What's Next** | A conversation with the `whats-next` bot ("Nexie"): it surveys the repo and the board, proposes what to do next and can create and dispatch cards. The same session is available from the assistant dock on every page ([assistant dock](assistant-dock.md)). A dot on the entry means it is waiting for your answer. |
| — | **Editor** | Author a bot: canvas, node inspector, source view and live diagnostics. Its start page lists the project's `.bot` files (built from the same places as the catalog, one row per bundle, capped at 500), **Open…**, **Import copy…** and your recent files. Open tabs fold under the entry. |
| — | **Runs** | Every run of this project, live and past: status, cost, duration, filters; open one for the run console (live graph, event log, diffs, commits, cost report). A badge counts runs paused for your input. **Analytics** (button on this page, `/insights`) shows cost over time and per-workflow run counts, failure rates and durations. |
| — | **Bots** | The catalog: every bot you can launch, with its origin and *enabled* flag; **New bot** opens the guided builder. |
| Operate | **Board** | The project's kanban of cards. Cards become runs when a bot is assigned and the card is in a ready column. **Refresh** re-reads every card from disk. |
| Operate | **Pipelines** | A control-center view of *root* runs across all bots in four lanes (Opened, In progress, Needs attention, Closed); pending human reviews can be answered from a card there. Shares the Board's store. |
| Operate | **Dispatcher** | The loop that turns ready cards into runs: its settings, running issues, retry queue (each row has *Retry now* and *Remove from queue*) and the *Won't dispatch* table. Start, pause and stop it here. A *Did not start* panel lists cards whose launch failed before a run existed (for example a failing `after_create` hook). |
| Operate | **Automations** | Schedules, webhooks and triggers that launch runs without you. |
| Extend | **Marketplace** | Browse, install and publish `.botz` bundles and plugins. |
| Extend | **Plugins** | Installed plugins (rewriters, MCP servers, skills), enable/disable, and load failures ([plugins](plugins.md)). |
| Extend | **Skills** | Your local skill library, usable by name from any workflow ([skills](skills-library.md)). |
| Manage | **Secrets** | API keys and named secrets, sealed locally ([secrets](secrets.md)). |
| — | **Sitemap** | The list of every page this server answers. An unknown address shows **Page not found** with links to Home and the Sitemap — it no longer falls back to Home. |

Cloud-only pages (Integrations, Admin) and the Config editor appear only on a
server that has them. The sidebar's **Preferences** button opens the Settings dialog: Backends
(the detected LLM credentials), Appearance and more.

### Bots versus the Editor's file list

They overlap on purpose, and show different things:

- **Bots** is the *catalog*: your own bots in this project — bundles with a manifest
  from `bots/`, `examples/` and `.botz/`, and the single `.bot` files in the project
  folder. Each row has an origin badge (Project, Project file); the number in the
  header counts the rows the page lists, and the Home *Bots* panel and the **Manage**
  dialog show the same set. Use it to *launch*, to read a bot's page, or to open its source.
  When the catalog fails to load, the Home panel shows a "Bot catalog unavailable"
  banner with a **Retry** button.
  The catalog and the Editor's list walk the roots by the same pruning rule
  (no hidden folders, `node_modules`, `testdata` or nested clones).
- The **Editor** lists *files*: it opens a path, edits it and saves it back.
  A **Project file** from the catalog has no bot page; it opens here.

### Open versus Import copy

- **Open… / `Ctrl+O`** picks a file from the project's lists (and examples), or opens
  any `.bot` file of the project by the path you type in *Open by path* — a file in
  `workflows/` or `docs/` that no list shows. Only workflow files open this way:
  any other path (a `.env`, a config, a `.bot.yaml`) is refused with
  `not_workflow_file` before the server reads it, so the Editor is no way to read
  the project's secrets. A recent-files entry the server refuses this way is removed
  from Recent when you open it, with a toast saying why. The tab is bound to that path, so
  **Save** writes over the original. If the list was cut at its cap, the picker
  says so (*Showing the first N files of M*).
- **Import copy from disk…** takes a file from *anywhere* on your disk, but only
  its text. The tab is unbound; the first save goes through **Save As** and
  creates a new project file — it never overwrites.

Details in [visual editor → Saving a file](visual-editor.md#saving-a-file-from-the-studio).

## Scenario 1 — run a file by hand

Goal: take a `.bot` file and see it run.

1. **Editor → Open…** and pick the file (or use **Bots** → the bot → *Open in
   editor*). Fix anything the live diagnostics flag; **Save**.
2. Press **Run** (top right). If it is greyed out, the tooltip says why: no LLM
   credential detected (the warning icon beside it opens **Preferences →
   Backends**), an unsaved empty buffer, or errors in the document.
3. The launch form (`/runs/new`) asks for the bot's variables, attachments and
   options (model, budget, sandbox, worktree). Press **Launch**.
4. The run console opens at `/runs/<id>`: a live graph, the event log, cost.
   A `human` node pauses the run and shows a form; answer it and the run goes on.
   The run also appears under **Runs**, and survives closing the tab.
5. When it ends, a bot that works in a `worktree: auto` checkout leaves its
   changes on a branch: review the diff and commits in the console and merge
   ([merge policy](merge-policy.md)).

From the **Bots** page, **Launch** on a bot's page skips steps 1–2.

## Scenario 2 — run a card from the board

Goal: hand a task to a bot through the Board and let the dispatcher run it.

1. **Board → + New issue.** Give it a title and a body (the bot reads both).
2. **Assign a bot.** Set the **Assignee** (the name of one of your project's
   bots, such as `my-helper`) or the **Bot** field, then save. *A card with
   neither is not run*: the zero-config dispatcher refuses to guess, and the card
   shows **⚠ no assignee — won't start**, plus a row in the Dispatcher's *Won't
   dispatch* table. An assignee that is not one of your bots (a person's name, a
   typo) is not run either: **⚠ no bot — won't start**.
3. **Move it to Ready** (drag the card to the Ready column). Ready and In progress are
   the columns the dispatcher takes cards from.
4. **A configured dispatcher launches Ready cards; start it.** Open **Dispatcher**. If it says *No
   dispatcher running*, press **Use default settings**: it builds the default
   configuration from your project's own bots (`bots/`) and starts it. A dispatcher that was
   configured before resumes by itself when the studio starts.
   With no dispatcher config at all, the studio launches Ready cards itself.
5. Within one poll (30 s by default) the card moves to **In progress** and shows
   a live run chip. Click it to open the run console; the run is also under
   **Runs**, and the console header says which card it came from.
6. When the run ends the card moves on and keeps a *Last run* record (status,
   error, a link to the run). A run that pauses for a human parks the card in
   *Awaiting input* until you answer.

If the card shows *did not start* instead, the launch failed before a run existed (a lifecycle hook such as `after_create` exited non-zero): the card's Ticket tab and the *Did not start* panel of the Runs and Dispatcher pages show the full error, and there is no run console for it.

If a card created from the CLI or by an assistant does not show up, press
**Refresh**. Mechanics and settings: [dispatcher](dispatcher.md),
[native tracker](native-tracker.md).
