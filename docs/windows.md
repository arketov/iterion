# Iterion on Windows

**Status:** describes `main`. The shell rules are read from `pkg/internal/hostshell`
and its callers; the PowerShell 5.1 behaviours in the limitations table were
tried on a Windows 11 machine (Windows PowerShell 5.1.26100, PowerShell 7.6.9).
Not covered: Windows containers (the sandbox is always a Linux container), and
cloud deployments, which run on Linux.

Iterion runs natively on Windows 11. It does not need WSL, Git for Windows or a
POSIX shell: the commands it starts on your machine go through PowerShell. This
page is the one place for what that means, what is different from Linux and
macOS, and the known gaps with their workarounds.

## What is supported

| Piece | Windows status |
|---|---|
| Desktop app | Portable `.exe` and a per-user installer, x64 and arm64; V1 builds are unsigned, so SmartScreen warns once. See [desktop](desktop.md). |
| CLI | A native `iterion.exe` (amd64 and arm64). See [install](install.md#cli). |
| Studio, runs, board, dispatcher | The same code as on Linux. |
| Tool nodes, hooks, plugin commands | Run under PowerShell by default — see below. |
| Sandbox | Needs Docker on the host; the commands that run *inside* the container stay `sh`/`bash`, because the container is Linux. |
| Backends | `claw` and `claude_code` work. The `grok` and `kimi` CLI-agent backends cannot gate a node with `permission: deny` on Windows — see the limitations. |

## Which shell runs your commands

One rule, in `pkg/internal/hostshell`, covers every command iterion starts on
the host: a tool node's `command:`, a `script:` without a `language:`,
dispatcher and schedule hooks, plugin lifecycle commands and
`.claude/settings.json` command hooks.

- **Default on Windows:** `pwsh` (PowerShell 7+) when it is on `PATH`, otherwise
  the built-in `powershell.exe` (5.1). Linux and macOS keep `sh`/`bash`.
- **Choose a shell yourself:** a dispatcher hook takes `shell:` (`sh`, `bash`,
  `pwsh`, `powershell` or a path); the environment variable `ITERION_SHELL`
  (a name or a path) overrides the default for everything else. The hook's
  `shell:` wins over `ITERION_SHELL`. Both are described in
  [dispatcher](dispatcher.md#hooks) and
  [environment variables](environment-variables.md).
- **A saved `sh` hook:** the dispatcher's default `after_create` hook has a
  PowerShell variant, and the studio migrates the old default of an older saved
  config. A hook you wrote yourself that is plainly `sh` and has no `shell:` is
  not rewritten: it runs under `sh` or `bash` when one is on `PATH`, and
  otherwise fails before starting with a message to add `shell:` or rewrite it.
  The studio writes that choice into the saved config (`shell: sh`), so it no
  longer depends on guessing from the text. The details are in
  [dispatcher](dispatcher.md#hooks).
- **A hook's `path:`:** a script file named by `path:` is run by the interpreter
  its `shell:`, its `#!` line or its extension names (`.ps1` PowerShell, `.sh`
  `sh`, `.py` `python` …), and an unknown one is an error that says what to
  set — it is no longer handed to PowerShell, which ignored the shebang and
  reported success without running the script.
- **When a hook fails:** the error, the Retry queue and the card's *Did not
  start* panel say which shell the hook ran under (name, path, and why it was
  chosen), even when no shell could be started.
- **A script's interpreter:** `language: powershell` (aliases `pwsh`, `ps1`) runs
  the body as a `.ps1` file with `-ExecutionPolicy Bypass`. The file is written
  as UTF-8 with a byte order mark, so Windows PowerShell 5.1 reads non-ASCII
  text in the body as written, and the body follows the same output and
  exit-code rules as a `command:`: its output is UTF-8, and a body whose last
  statement is a failing program fails the node with that program's exit code.
  `language: sh` or
  `bash` runs that shell wherever it is found; on Windows it must be on `PATH`,
  and a missing one is an error that names the shell, not a silent fallback. An
  empty `language:` follows the platform shell above.
- **Nothing found:** the error lists what was searched and suggests
  `winget install Microsoft.PowerShell`.
- **Output:** the engine turns PowerShell's CRLF line ends into LF, so a
  variable built from a tool node's stdout reads as it would from `sh`; the
  rest of what it normalises is in the portability rule below.
- **Dry run:** there is no syntax check for PowerShell bodies. When a body
  would run under PowerShell, the dry run lists it as unchecked instead of
  reporting it valid.
- **Long scripts:** a body of more than about 12000 characters is streamed to
  PowerShell on stdin rather than passed on the command line.

To write bodies that work on Windows, or on both systems, follow the
[portability rule](workflow_authoring_pitfalls.md#shell-portability-for-tool-nodes):
it lists the `sh` forms that do not survive PowerShell and when to put the logic
in a `script:` with an explicit `language:`.

## No console windows for child processes

The desktop app is a GUI program, and the studio daemon it starts has no console
either. Windows opens a visible console window for every console program such a
process starts (`git`, `pwsh`/`powershell` for hooks and tool nodes, the `claude`
and `codex` CLIs, `node`, MCP stdio servers, plugins), so a run that shells out
used to show a burst of terminals that flashed and closed. Iterion now keeps
them all windowless; pipes, exit codes and cancellation behave as before, and
Linux and macOS are unchanged.

- **A hidden console for the studio.** `RunStudio` (the studio, the desktop
  daemon and the desktop's in-process fallback) creates a console with no window
  at startup when the process has none. Every console program the studio starts
  inherits it and opens no window, including those started by code that never
  went through the seam below (the `claw` backend's bash tool, hooks and MCP
  stdio servers run inside the studio). The new console exists for a few
  milliseconds before it is hidden, so a single flash at startup is possible.
- **A flag on each child.** `proc.Command` and `proc.CommandContext`
  (`pkg/internal/proc`) replace `exec.Command` / `exec.CommandContext` in
  production code and start the child with `CREATE_NO_WINDOW` whenever this
  process has no console. They are the fallback if the hidden console could not
  be created, and what covers a run that executes outside the studio process.
  A process that has a console, such as `iterion run` in a terminal, adds no
  flag: its children share the terminal and its Ctrl+C.
- **Detached runners.** The studio's detached run launcher and the operator MCP
  spawner assign their own process attributes, and those carry
  `CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW` (operator MCP: `CREATE_NO_WINDOW`).
- **Override.** `ITERION_CHILD_WINDOWS=hide` adds the flag even when a console
  exists; `show` never adds it and stops the studio from creating the hidden
  console. Unset or `auto` is the behaviour above.
- **Not covered on purpose.** Opening the browser or Explorer (they start GUI
  programs) and the macOS and cron-only calls.
- **Adding a shell-out.** Use `proc.Command*`; `TestNoRawExecCommand`
  (`pkg/internal/proc/nowindow_guard_test.go`) fails on a raw `exec.Command` in
  `pkg/` unless the file is listed there with a reason. The `codex` SDK fork in
  `third_party/` applies the same rule in its own Windows files (see its
  `FORK.md`).

## Known limitations and workarounds

| What you see | Why | What to do |
|---|---|---|
| Updating a bot's dependencies from the studio answers 400, "dependency transactions require filesystem identity checks on this platform". | The transaction needs a filesystem identity check that is built for Linux and macOS only (`pkg/server/assistant_dependency_rename_other.go`). |  No workaround on Windows yet; update the pin on a Linux or macOS host. |
| A node with `permission: deny` on the `grok` or `kimi` backend is refused on Windows. | The gate hook is quoted for a POSIX shell and the backend's home is linked with symlinks, which an unprivileged Windows process cannot create. Running such a node ungated would fail open, so iterion refuses. | Use `permission: off`, or the `claude_code` or `claw` backend, which gate in-process. |
| Cancelling a run can leave a process of a tool node running. | Windows has no process-group signal; cancellation kills the command and the descendants found through their parent-process links (`pkg/internal/proc`). A process whose intermediate parent has already exited (a double-fork daemon) is not found. | End leftover processes in Task Manager, or `taskkill /F /T /PID <pid>`. |
| `iterion schedule install` fails. | It writes the managed block into the host `crontab`, which Windows does not have. `schedule install --print` shows the block without needing one. | Point Windows Task Scheduler at `iterion schedule run <name>`, the command a cron line calls (`schedule install --print` shows the exact line; not tried here). The manifest commands (`add`, `list`, `remove`) do not need `crontab`. See [scheduling](scheduling.md). |
| A quote inside an argument to a program is lost: `node -e "…" 'say "hi"'` receives `say hi`. | Before PowerShell 7.3 (so in 5.1), embedded double quotes are stripped when calling a native program. | Use PowerShell 7.3 or later, or pass such values through an environment variable or a file. |
| "Access is denied" when deleting, renaming or saving a file in a folder synced by OneDrive. | The sync client or an antivirus scanner briefly holds the file open without delete sharing. Iterion reads its board's card files with delete sharing, but cannot control a handle another program holds. | Keep the project, its worktrees and the store outside OneDrive-synced folders, or pause syncing while a run is active. |
| `git` fails with "Filename too long" in a working copy. | A deep tree, a long OneDrive folder name and a worktree path pass 260 characters, which git on Windows refuses by default. | `git config --global core.longpaths true`, or per repository without `--global`. A run's workspace (`worktree: auto`) is a git worktree kept in the project's store, in a folder named after the project's whole path, so a deep project path lengthens every path inside it; the repository's setting covers those worktrees too. |
| A board card does not start: the seeding hook (`git worktree add`) ends with exit code 128 and `fatal: '$GIT_DIR' too big`. | `git worktree add` registers the workspace under `<project>/.git/worktrees/<folder name>`, and git on Windows refuses a gitdir of about 220 bytes; `core.longpaths` does not lift this limit. The dispatcher names workspace folders with at most 42 characters, so what is left over is the project path itself (a Cyrillic folder name counts two bytes per letter). A folder with the old long name that was never seeded is replaced by a short-named one on the next dispatch. | Move the project to a shorter path, for example `C:\src\<project>`. See [the workspace lifecycle](dispatcher.md). |

## Working on iterion itself

Iterion's own contributor setup assumes devbox, which is a Nix tool for Linux
and macOS and does not exist on Windows. On Windows install the tools yourself:
Git, the Go version in `go.mod`, Node.js (its `corepack` provides `pnpm`) and
[`task`](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`).
Run `task <name>` without the `devbox run --` prefix. Several targets call
`bash scripts/…` (for example `openapi:gen` and `openapi:check:dry`), so
contributing also wants a `bash` on `PATH`, such as the one from Git for
Windows. That requirement is for building iterion, not for running it. The
steps and the gate are in [development](development.md).

## Building the desktop app on Windows

Build on Windows: Wails cannot cross-compile to it from another system
([desktop build](desktop-build.md#cross-compiling)). The Taskfile targets
`desktop:build:windows:amd64` and `desktop:build:windows:arm64` are what CI runs
on `windows-latest`. By hand the steps are:

1. Build the studio: in `studio`, `corepack pnpm install` and
   `corepack pnpm exec vite build`, then copy `studio\dist\*` into
   `pkg\server\static` (the `studio:build` target does this).
2. Install the Wails CLI at the version in `go.mod`:
   `go install github.com/wailsapp/wails/v2/cmd/wails@<version>`.
3. In `cmd\iterion-desktop` run
   `wails build -tags desktop -platform windows/amd64 -clean -trimpath -skipbindings -s -o iterion-desktop-windows-amd64.exe`.
   Add `-nsis` for the installer; it needs NSIS (`makensis`) on `PATH`.

`cmd\iterion-desktop\build` is a symlink to `..\..\build`. A checkout made without
the right to create symlinks leaves a plain text file there, and Wails then fails
to create `build\bin`. Delete that file and make a junction, which needs no
administrator rights: `mklink /J cmd\iterion-desktop\build build` from the
repository root. Version and commit are stamped through `-ldflags`, as in the
Taskfile's `LDFLAGS`.

## Where your data lives

Runs, the board and secrets are kept in a store outside the project folder, by
default under `%USERPROFILE%\.iterion\` (`ITERION_HOME` moves it); the desktop
app's own settings and project list are in `%APPDATA%\Iterion\`. How iterion
chooses the store and how to move it are on
[where iterion keeps data](where-iterion-keeps-data.md);
the desktop app itself is described in [desktop](desktop.md).
