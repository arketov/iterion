# Your own bot on the board

From a bot you wrote to a run started by a card on the studio board: the
bundle, the `dispatch_vars` that carry the card into the bot, the dispatcher,
the card, and what to do when nothing happens.

Status: the manifest and bot below were validated with `iterion validate` and
`validate --exec`; the studio steps are written from the studio and dispatcher
code, not from a clean-machine click-through.

You need the desktop studio (or `iterion studio`) open on a project folder, and
one credential iterion can detect ([backends](backends.md)). Everything here is
the local, native board; the dispatcher itself is described in
[dispatcher](dispatcher.md).

## How a card becomes a run

Moving a card to `ready` starts nothing. A **dispatcher** has to be running; it
polls the board, picks a card in an eligible state, resolves the card's bot,
turns the card into the bot's input vars, and runs it in a workspace of its own.
Three things must line up, and each is a step below:

1. the bot is a **bundle** the dispatcher can find, whose manifest says how to
   fill its vars from a card (`dispatch_vars`);
2. a dispatcher is **running** (the Dispatcher page);
3. the card **names the bot**.

## 1. Make the bundle

A bot reachable from a card is a folder, not a lone `.bot` file. Put it in
`bots/<name>/` of the project the studio has open (the studio also looks in
`examples/` and `.botz/`; iterion ships no bots of its own, so a card can only be
run by a bot of your project). The quickest start:

```bash
iterion bots create my-helper --backend claude_code --model anthropic/claude-sonnet-5-5
```

or **New bot** on the studio's Bots page. Either writes `bots/my-helper/` with
`manifest.yaml`, `main.bot`, `README.md`, a `.gitignore`, `presets/example.md`
and empty `prompts/`, `skills/`, `attachments/`. The name must start with a
lowercase letter, then lowercase letters, digits and `-` (2 to 64 characters),
and be unique across everything the studio discovers.

A single `.bot` file picked from the file tree can run from the Launch form but
is **not offered on a card**: the dispatcher routes a card to a bot by its
catalogue name.

## 2. Declare the input: `dispatch_vars`

A scaffolded bot has no input var and its manifest has no `dispatch_vars`, so a
card would reach it with nothing. Add both. In `bots/my-helper/main.bot`:

```iter fragment
dsl: 2

vars:
  ## Filled from the card by the manifest's dispatch_vars.
  task: string = ""

prompt kickoff:
  Your task:

  {{vars.task}}

  Carry out your mission now. Work end-to-end: investigate, act, and
  verify before reporting. Set mission_complete=true only when the
  mission is genuinely done.

agent campaign:
  backend: "claude_code"
  model: "anthropic/claude-sonnet-5-5"
  input: mission_report
  output: mission_report
  system: mission
  user: kickoff
```

(The rest of the scaffold — the `mission_report` schema, the `mission` prompt
and the `workflow` with `worktree: auto` — stays as generated.) In
`bots/my-helper/manifest.yaml`, keep what the scaffold wrote and add at the end:

```yaml
dispatch_vars:
  task: |-
    {{issue.title}}

    {{issue.body}}
```

Rules that bite:

- **Keys are var names.** `task` must be declared in the workflow's `vars:`. A
  key that names no var is **silently dropped at dispatch time**; `iterion
  validate bots/my-helper` warns `C200` for it. Run it after every edit.
- **Values are templates**, rendered once per card: `{{issue.title}}`,
  `{{issue.body}}`, `{{issue.identifier}}` and the rest of the
  [dispatch templates](dispatcher.md#dispatch-templates). `|-` keeps the YAML
  block free of a trailing newline.
- **`dispatch_vars` replace the dispatcher's global vars for this bot** rather
  than adding to them. Declare every var you want filled.
- **What the card's *Bot* tab → *Arguments* adds** (`bot_args`) is merged on top
  of `dispatch_vars`, key by key, and wins.
- Check the dry run before paying for one: `iterion validate bots/my-helper
  --exec` renders the prompts with the declared vars and names anything left
  unresolved ([validate without running](quickstart.md#check-before-you-run)).

## 3. Start a dispatcher

Open the studio's **Dispatcher** page.

| What the page shows | Meaning | What to do |
|---|---|---|
| *No dispatcher running* with a **Use default settings** button | `state: idle`, `has_config: false` — nothing has ever been configured for this project | Press **Use default settings** |
| *No dispatcher running*, no such button | `state: idle`, `has_config: true` — a config exists but the dispatcher is stopped | Toolbar → **Start** (**Settings** first if you want to change it) |
| Summary card, *Running*, *Retry queue*, *Won't dispatch* | `state: running` (or `paused`: new dispatches are suspended, runs in flight continue; **Resume** in the toolbar) | Nothing; go to step 4 |
| An error message (`last_error`) | `state: error` — the last start failed | Read it; usually a path or a bot that does not resolve in **Settings** |

**Use default settings** builds the same dispatcher as the zero-config
`iterion dispatch`, saves it and starts it at once: the native board, polling
every 30 s, two runs at a time, `require_assignee` on, **your project's `bots/`,
`examples/` and `.botz/`** as the bots a card can be given (no built-in bots, no
fallback workflow: with no bot yet, cards wait marked *no bot*), and an `after_create` hook
that seeds each card's workspace from your project at its current `HEAD`
(`git worktree add --detach`; PowerShell on Windows, `sh` elsewhere). It is
refused (409) when a config already exists. Details:
[quick start](dispatcher.md#quick-start-zero-config).

The idle stub is not an error and the board is not broken: until a dispatcher
runs, `ready` is only a column. A dispatcher that was running comes back by
itself when the studio restarts on that project; press **Stop** in the toolbar
before you quit if you do not want that.

**`dispatch_vars` are read at every dispatch.** The per-bot vars come from the
bot's own manifest each time a card is dispatched, so a bot created — or a
`dispatch_vars` block edited — after the dispatcher was set up reaches its next
card with no change to the settings and no restart. To bind a different value
for one bot without touching its manifest, add an `assignee_dispatch:` entry for
it (Settings → Routing → *Per-bot input vars*;
[per-assignee dispatch overrides](dispatcher.md#per-assignee-dispatch-overrides)).

## 4. Put the bot on the card

Open the board, create a card (title = what to do, body = the details; both
become `task`), and name the bot one of two ways:

- **Bot tab → Bot**: a searchable list grouped by origin (project, team,
  platform). It lists catalogue bots only, hides disabled ones, and **overrides**
  the assignee for this card. The *Arguments* below it become `bot_args`.
- **Ticket tab → Assignee**: a free-text field with suggestions. Write a
  project bot's name. Case and `_` for `-` do not matter (`My_Helper` is
  `my-helper`, for the routing and for the bot's `dispatch_vars` alike). An
  assignee that is **not** a bot of the project — a person's name, a typo — does
  not run anything: the card shows **no bot — won't start**.

Then move the card to `ready`. At the next poll (up to 30 s) the dispatcher
claims it, moves it to `in_progress`, and a live chip shows the run on the card.
The run console opens from the card and records the ticket it came from. When
the run ends cleanly the card goes to `review`.

What a card with no bot does: with `require_assignee` on (the default
configuration), a ready card with neither a bot nor an assignee is **not run**.
It shows **no assignee — won't start** on the card and in the Dispatcher page's
*Won't dispatch* table. A card whose *Bot* names something the catalogue cannot
resolve, or a disabled bot, shows **won't dispatch** with the reason in the same
table.

Where the finished work is, how to look at it and how to merge or throw it away:
[where the result goes](dispatcher.md#where-the-result-goes).

## Things that go wrong

| Symptom | Cause | Fix |
|---|---|---|
| Card is `ready`, nothing happens, Dispatcher page says *No dispatcher running* | No dispatcher started (`idle`) | Step 3 |
| Card badge **no assignee — won't start** | `require_assignee` and the card names no bot | Set *Bot* (or *Assignee*) on the card |
| Card badge **no bot — won't start** (Dispatcher page: *no bot (artem)*) | `require_assignee` and the *Assignee* is not a bot of the project (a person's name, a typo), or the bot is disabled on the Bots page | Pick the bot in the *Bot* tab, fix the assignee's spelling, or enable the bot |
| **won't dispatch — bot "x" can't be resolved / has no active dispatch route** | Name typo, bot not in a discovered folder, or the bot is disabled in the catalogue | Check the name against the Bots page; enable the bot; keep the bundle in `bots/` |
| The bot runs but acts as if given no task | `dispatch_vars` missing in the bot's manifest, or keyed to an undeclared var (`C200`) | `iterion validate bots/my-helper`; fill *Arguments* by hand; see step 3 |
| Retry queue, or the *Did not start* panel, or the card badge *did not start: after_create hook…* | The workspace seed failed: `git` not on the daemon's `PATH`, the project folder not readable, a hand-written hook in the wrong shell dialect | Read the stderr tail and the *Shell:* line (which shell ran the hook) in *Show full error*; [Hook failure](dispatcher.md#hook-failure). A failed `after_create` is run again, in a recreated folder, on the next attempt |
| `sh: command not found` or `/dev/null` in a hook on Windows | A custom hook written for `sh` but run by PowerShell, the Windows default | Write it in PowerShell, or name the shell with `shell:` / `ITERION_SHELL` ([Hooks](dispatcher.md#hooks)). A `path:` to a `.sh` file needs `sh` on Windows, or `shell:`/a `.ps1` instead |
| Settings **Workflow path** rejected or not found | The path does not exist: a relative one is taken from your project root | Pick the bot from the list (the studio writes its full path) or fix the path |
| The card sits in `review` and your project folder is unchanged | By design: the result is the branch `iterion/run/<id>`, not a merge | [Where the result goes](dispatcher.md#where-the-result-goes) |
| The card keeps retrying | The run fails; the Retry queue shows the error and the attempt | Fix the cause, then press *Retry now* on its Retry queue row instead of waiting out the backoff; *Remove from queue* stops it (the card moves to `blocked`, reopen it to try again). Otherwise after `agent.max_attempts` (10) the card moves to `blocked` |

## Where to go next

- [Dispatcher](dispatcher.md) — states, the polling tick, retries, hooks,
  routing by assignee, the zero-config preset.
- [Native tracker](native-tracker.md) — the board and its states.
- [Bundles](bundles.md) — the manifest and the `.botz` archive.
