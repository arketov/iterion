# Where iterion keeps data

**Status:** describes the local studio, desktop app and CLI on `main`; checked
against `pkg/store/storedir.go`, `pkg/cli/studio.go`, `pkg/skilllib`,
`pkg/plugin`, `cmd/iterion-desktop` and a Windows 11 install. Not covered: the
cloud, whose data lives in MongoDB and S3 ([cloud architecture](cloud-architecture.md)).

Iterion writes in five places. Your own files stay in your project; everything
the tool generates — runs, the board, settings — goes to a *store* outside it by
default, so a project folder is not littered with state. This page is the map:
what lives where, how iterion picks the store, and how to move it. New to the
vocabulary? Read [Core concepts](core-concepts.md) first.

## The map

`~` is your home folder (`C:\Users\<you>` on Windows). `<key>` is the project's
absolute path with every `\`, `/` and `:` turned into `-` and a leading `-`
added, so `C:\Users\Anna\shop` becomes `-C--Users-Anna-shop`. Different
paths get different keys, a clone included — with one caveat: `\`, `/` and `:` all become
`-`, so `C:\a-b` and `C:\a\b` collide, and `c:\` and `C:\` differ. Open the project
the same way each time.

| Place | What is in it | Who writes it |
|---|---|---|
| **Your project folder** — `<project>/` | Your `.bot` files, bot bundles (`bots/`, `examples/`, `.botz/`), the rest of your code. Runs that use `worktree: auto` add branches to its git repository. | You, the studio's **Save**, `iterion bots create` |
| **`.iterion/authoring/` beside a saved file** — for `bots/foo/main.bot`, `bots/foo/.iterion/authoring/` | The studio's save working state: a lock and recovery copies of the file being saved. There can be several, one per folder you saved a file in. It carries its own `.gitignore`, so git shows nothing of it. See [authoring lock](authoring-lock.md). | The studio, on every save |
| **The project store** — `~/.iterion/projects/<key>/` by default | `runs/` (every run: `run.json`, `events.jsonl`, artifacts), `dispatcher/` (the board: `board.json`, `issues/`, `events.jsonl`; the dispatcher's settings in `dispatcher.json`; bots extracted for the dispatcher in `bots/`), `worktrees/` (a checkout per `worktree: auto` run), `marketplace/`, `secrets.json` (the project's sealed secrets), `skills/` (project skills). A project's `memory/` and `scratch/` are kept under the same key in the iterion home, even when the store itself is project-local (rule 2 below). | The studio, the CLI, the dispatcher |
| **The iterion home** — `~/.iterion/` | Things that belong to *you*, not to a project: `plugins/` and `plugins.yaml` (installed plugins and which are enabled), `skills/` (your skill library), `secrets.json` (global secrets), `cli-auth.json` (the login of `iterion remote login`), `model-specs-cache.json`, and the `projects/` folder above. | The CLI, the studio, the desktop app |
| **`~/.iterion/daemons/`** | One `<key>.json` per project server the desktop app started (its address, pid, project folder and start time) and a `<key>.log` beside it; the desktop app reads them to reattach. `~/.iterion/desktop.json` points at the most recent one. | The desktop app |
| **`%APPDATA%\Iterion\`** (Windows; the user config folder elsewhere) | `config.json` — window size, the list of recent projects and the current one, updater settings — plus the lock files that keep one desktop instance (and one server per project) running, and `default-project/`, the empty folder the studio opens before you pick a project. | The desktop app (the workspace studio shares the file) |

Two practical consequences:

- **Nothing in the project is needed to run it elsewhere.** Move your `.bot`
  files or bundles; the runs and the board stay behind in the store.
- **Deleting a project folder does not delete its runs.** They remain under
  `~/.iterion/projects/<key>/` until you remove that folder (or run
  `iterion clean --with-runs`; plain `iterion clean` only reclaims per-run
  worktrees and scratch).

## How iterion chooses the store

The studio, `iterion run`, `iterion dispatch` and the other local commands all
ask the same function, `store.ResolveStoreDir`
([source](../pkg/store/storedir.go)), with a starting folder: the
studio's project folder, and for CLI commands the **current directory** (not the
bot file's folder). It takes the first rule that applies:

1. **You named one.** `--store-dir <dir>` (or the desktop project's own store
   setting) is used exactly as given.
2. **The project already has a real `.iterion` folder.** `<project>/.iterion/`
   is used — but only if it *looks like a store*: it must contain `runs/` or
   `dispatcher/`, or a file named `.iterion-store`. A folder that merely
   exists does not count. This is why the studio's `.iterion/authoring/`, or a
   `.iterion/plans/` that an agent happened to write, never hijacks the choice.
3. **Otherwise, the project's slot in your home:**
   `<iterion home>/projects/<key>/` — normally `~/.iterion/projects/<key>/`.

Rule 3 is the default, so a project nobody configured keeps its state in
`~/.iterion/projects/<key>/` and the project folder stays clean. Rule 2 is the
opt-in for people who want runs and the board *inside* the repository, next to
the code: create `<project>/.iterion/runs/` (or an empty `.iterion-store` file)
once, and from then on that folder is the store. The old behaviour — walking up
to a stray `.iterion` in a parent folder such as `~` and sharing it across
unrelated projects — is gone on purpose.

The consequence worth remembering: **the CLI and the studio share runs only if
they resolve to the same store.** Both start from a folder, so run the CLI from the
same folder the studio serves (a subfolder gives a different key), or pass the same `--store-dir` to both.

## Changing it

| You want | Do |
|---|---|
| One command or studio to use a specific store | `--store-dir <dir>` — `iterion run`, `iterion studio`, `iterion dispatch`, `iterion inspect` and the other local commands take it. Pass the same value to every command that must see the same runs. |
| Every project's slot (and all of `~/.iterion/`) somewhere else — another disk, a synced folder | Set the `ITERION_HOME` environment variable to the folder you want. It replaces `~/.iterion` as a whole: plugins, skills, global secrets and the `projects/` slots move with it. The desktop app reads it too, for `daemons/` and `desktop.json`. |
| A project's state inside the repository | Create `<project>/.iterion/runs/` (rule 2) and add `.iterion/` to the project's `.gitignore` unless you want it versioned. |
| A single desktop project on its own store | The desktop app passes the project's `store_dir` from `config.json` to that project's server as the explicit override (rule 1). There is no settings screen for it; edit the file with the app closed. |

Details that bite:

- `ITERION_HOME` only counts as *your* choice when it is set in the environment
  iterion was started from. A value written in a project's `.env` file is still
  used for locating data but does not give that project's code the trust that
  an operator-set value has (for example for sandbox mounts). See
  [environment variables](environment-variables.md).
- With no home folder at all (a bare CI container) iterion falls back to
  `<temp>/iterion-data`.
- `%APPDATA%\Iterion\` is **not** moved by `ITERION_HOME`; it is the OS user
  config folder.
- The cloud login of `iterion remote login` (`cli-auth.json`) is always read
  from the real `~/.iterion/`, whatever `ITERION_HOME` says.

## Seeing it for yourself

From PowerShell on Windows:

```powershell
Get-ChildItem $HOME\.iterion\projects          # one folder per project you have opened
Get-ChildItem $env:APPDATA\Iterion             # desktop settings and locks
```

A run's own files are under `<store>/runs/<run-id>/`; the exact formats are in
[persisted formats](persisted-formats.md), and the layout from the engine's side
is in [architecture](architecture.md#local-store). Board cards are plain JSON in
`<store>/dispatcher/issues/` — see [native tracker](native-tracker.md#storage-layout).
