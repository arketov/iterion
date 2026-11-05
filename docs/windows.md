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
| CLI | A native `iterion.exe` (amd64). See [install](install.md#cli). |
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
- **A script's interpreter:** `language: powershell` (aliases `pwsh`, `ps1`) runs
  the body as a `.ps1` file with `-ExecutionPolicy Bypass`. `language: sh` or
  `bash` runs that shell wherever it is found; on Windows it must be on `PATH`,
  and a missing one is an error that names the shell, not a silent fallback. An
  empty `language:` follows the platform shell above.
- **Nothing found:** the error lists what was searched and suggests
  `winget install Microsoft.PowerShell`.
- **Output:** the engine turns PowerShell's CRLF line ends into LF, switches off
  colours and progress records, and keeps the exit code of the last native
  program, so a variable built from a tool node's stdout reads as it would from
  `sh`.
- **Dry run:** there is no syntax check for PowerShell bodies. When a body would run under PowerShell, the dry run lists it as unchecked instead of reporting it valid.
- **Long scripts:** a body of more than about 12000 characters is streamed to
  PowerShell on stdin rather than passed on the command line.

To write bodies that work on Windows, or on both systems, follow the
[portability rule](workflow_authoring_pitfalls.md#shell-portability-for-tool-nodes):
it lists the `sh` forms that do not survive PowerShell and when to put the logic
in a `script:` with an explicit `language:`.

## Known limitations and workarounds

| What you see | Why | What to do |
|---|---|---|
| Updating a bot's dependencies from the studio answers 400, "dependency transactions require filesystem identity checks on this platform". | The transaction needs a filesystem identity check that is built for Linux and macOS only (`pkg/server/assistant_dependency_rename_other.go`). | No workaround on Windows yet; update the pin on a Linux or macOS host. Tracked as a bug. |
| A node with `permission: deny` on the `grok` or `kimi` backend is refused on Windows. | The gate hook is quoted for a POSIX shell and the backend's home is linked with symlinks, which an unprivileged Windows process cannot create. Running such a node ungated would fail open, so iterion refuses. | Use `permission: off`, or the `claude_code` or `claw` backend, which gate in-process. |
| Cancelling a run can leave a child of a tool node running. | Windows has no process-group signal; cancellation kills the direct child only (`pkg/internal/proc`). | End leftover processes in Task Manager, or `taskkill /F /T /PID <pid>`. |
| `iterion schedule install` fails. | It writes the managed block into the host `crontab`, which Windows does not have. `schedule install --print` shows the block without needing one. | Point Windows Task Scheduler at `iterion schedule run <name>`, the command a cron line would call (not tried here). The manifest commands (`add`, `list`, `remove`) do not need `crontab`. See [scheduling](scheduling.md). |
| Non-ASCII text in a `language: powershell` body comes out garbled on Windows PowerShell 5.1. | The script file is written as UTF-8 without a BOM, and 5.1 reads a BOM-less file in the legacy code page. PowerShell 7 reads it as UTF-8. | Install PowerShell 7 (iterion prefers `pwsh`), or keep the body ASCII and read non-ASCII data from a file with an explicit `-Encoding UTF8`. |
| A quote inside an argument to a program is lost: `node -e "…" 'say "hi"'` receives `say hi`. | Before PowerShell 7.3 (so in 5.1), embedded double quotes are stripped when calling a native program. | Use PowerShell 7.3 or later, or pass such values through an environment variable or a file. |
| "Access is denied" when deleting, renaming or saving a file in a folder synced by OneDrive. | The sync client or an antivirus scanner briefly holds the file open without delete sharing. Iterion reads its board's card files with delete sharing, but cannot control a handle another program holds. | Keep the project, its worktrees and the store outside OneDrive-synced folders, or pause syncing while a run is active. |
| `git` fails with "Filename too long" in a working copy. | A deep tree, a long OneDrive folder name and a worktree path pass 260 characters, which git on Windows refuses by default. | `git config --global core.longpaths true`, or per repository without `--global`. A run's workspace (`worktree: auto`) is a git worktree kept in the project's store, in a folder named after the project's whole path, so a deep project path lengthens every path inside it; the repository's setting covers those worktrees too. |

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

Runs, the board, settings and the desktop's project list are kept in a store
outside the project folder, by default under `%USERPROFILE%\.iterion\`. Which folder, how iterion chooses it and
how to move it are on [where iterion keeps data](where-iterion-keeps-data.md);
the desktop app itself is described in [desktop](desktop.md).
