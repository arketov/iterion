# Desktop App

A native desktop build (Wails v2) wraps the visual editor in its own window with multi-project switching, OS-keychain credential storage, first-run onboarding, and Ed25519-signed auto-update. Two binaries ship side-by-side: `iterion` (CLI) and `iterion-desktop` (this app).

The desktop app embeds the same studio SPA, so the interface is identical to the [web studio](visual-editor.md) — same canvas, run console, board, and dispatcher — minus the browser chrome, plus a native window, project switcher, and OS keychain:

![Iterion studio interface — identical in the desktop window](images/studio/home.png)

## Install

Pick the artefact that matches your OS from [the latest GitHub Release](https://github.com/SocialGouv/iterion/releases/latest) (filenames start with `iterion-desktop-`). Each tag publishes:

| Platform | File | Size | Notes |
|---|---|---|---|
| Linux x86_64 | `iterion-desktop-linux-amd64.AppImage` | ~110 MB | Self-contained, click-to-run |
| Linux x86_64 | `iterion-desktop-linux-amd64.deb` | ~16 MB | Debian/Ubuntu/Mint package — `apt`-managed install + uninstall, declares deps |
| Linux x86_64 | `iterion-desktop-linux-amd64.tar.gz` | ~16 MB | Raw binary + README; needs `libwebkit2gtk-4.1-0` + `libgtk-3-0` + `libsoup-3.0-0` |
| Linux arm64 | `iterion-desktop-linux-arm64.{AppImage,deb,tar.gz}` | same | same |
| macOS Intel + Apple Silicon | `iterion-desktop-darwin-universal.zip` | ~80 MB | Universal `.app` (lipo'd, runs natively on both archs) |
| Windows x64 | `iterion-desktop-windows-amd64.exe` | ~50 MB | Portable single executable |
| Windows x64 | `iterion-desktop-windows-amd64-installer.exe` | ~50 MB | NSIS installer (per-user, Start Menu integration) |
| Windows arm64 | `iterion-desktop-windows-arm64.{exe,-installer.exe}` | same | same |

### Linux

**AppImage** (no system deps):
```bash
chmod +x iterion-desktop-linux-amd64.AppImage
./iterion-desktop-linux-amd64.AppImage
```

**Debian/Ubuntu/Mint** (.deb — apt manages deps + uninstall):
```bash
sudo apt install ./iterion-desktop-linux-amd64.deb
iterion-desktop
```

**Raw binary** (smaller, requires WebKit + GTK runtime):
```bash
# Debian/Ubuntu/Mint/Pop!_OS:
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0 libsoup-3.0-0
# Fedora/RHEL:
sudo dnf install gtk3 webkit2gtk4.1 libsoup3

tar -xzf iterion-desktop-linux-amd64.tar.gz
chmod +x iterion-desktop
./iterion-desktop
```

### macOS

The simplest path is Homebrew:
```bash
brew tap socialgouv/iterion https://github.com/SocialGouv/iterion
brew install --cask iterion-desktop
open -a Iterion
```

Or install manually from the release ZIP:
```bash
unzip iterion-desktop-darwin-universal.zip
xattr -d com.apple.quarantine Iterion.app   # one-off Gatekeeper unblock (V1 builds are unsigned)
open Iterion.app
```

You can also drag `Iterion.app` to `/Applications/` for a permanent install.

### Windows

- **Portable** : double-click `iterion-desktop-windows-amd64.exe`. SmartScreen will warn ("Unknown publisher" — V1 is unsigned) → "More info" → "Run anyway".
- **Installer** : run `iterion-desktop-windows-amd64-installer.exe` for an install with Start Menu shortcut; it also installs `iterion.exe` (the CLI the desktop spawns for the board and `ask_user` MCP servers) next to the desktop. The portable `.exe` needs `iterion.exe` in the same folder or `ITERION_BIN` for those.

## First launch

The desktop app boots into a Welcome wizard that asks you to:
1. Pick or create a project folder (a directory containing `.bot` files).
2. Configure API keys (stored in the OS keychain — Keychain on macOS, Credential Manager on Windows, Secret Service on Linux). Skippable; you can also rely on environment variables in your shell.
3. Verify external CLIs (`claude`, `codex`) detection — only needed if you use `backend: "claude_code"` or `backend: "codex"` in your workflows.

After onboarding the studio opens on your project. The sidebar shows the name of the current project (read-only). Switch projects with the project switcher (Cmd/Ctrl+P or the native menu): the window re-attaches to that project's own studio server and reloads, so files, bot catalog, board, runs and dispatcher all follow. The browser studio (`iterion studio`) has no in-window switcher — it serves one project; open another tab or window with its own server for another project.

Connections that are not open are listed under **Open connection** in the **+** menu of the tab bar (a click opens one as a tab). Each row has a **×** on its right, always visible and named *Remove connection <name>*, that forgets the connection: useful for a stale entry, such as a project folder you moved. It asks for confirmation first, then drops the entry from the list and the recent projects and stops the project's background daemon; the project's files on disk are never deleted. Removing a cloud connection signs you out of that cloud first, and is refused, with the reason shown in a banner under the tab bar, when the sign-out cannot be confirmed; nothing on the cloud server is deleted. An open connection is closed from its tab first. The browser studio has no tab bar, so no **×**.

The editor home (no tab open) lists the project's `.bot` files, offers **Open…** (the searchable picker, `Ctrl/Cmd+O`, also reachable as **Open file…** in the command palette, `Ctrl/Cmd+K`) and **Import copy…** for a file from disk, and keeps the Recent and Bots lists. A project or recent file is read before its tab opens: one that no longer exists on disk opens no tab and is removed from Recent. Reloading the window (`Ctrl/Cmd+R`) keeps the focused project tab, the tab order and each project's page (route) where you left them. The project list is built from the same roots as the bot catalog (`bots/`, `examples/`, `.botz/`, or your `--bots-path` roots) plus the `.bot` files directly in the project root (no recursion): a multi-file bot is one row (its `main.bot`), single files form a separate group, nested git checkouts and worktrees are never entered, a bundle directory with a manifest but no `main.bot` is reported in a warning instead of vanishing, and the list is capped at 500 entries, bundles first (`ITERION_STUDIO_FILES_LIMIT`, see [environment variables](environment-variables.md)); the editor home and the **Open…** picker both say when the cap cut the list. A file outside those places opens by its path (**Open…** → *Open by path*), bound to that path.

## Auto-update

Builds from this repository ship **without an update key**: the key inherited from upstream verified only SocialGouv/iterion releases, whose private half we do not hold, so it was removed. The update channel is inactive: Settings → Updates → "Check for updates" answers `updater: public key not configured`, the background check logs the same message at startup and every 4 hours, and nothing is downloaded. Once this repository embeds its own key, the app polls its releases every 4 hours and Settings → Updates installs on request; manifests and artefacts are Ed25519-signed. See [desktop-distribution.md](desktop-distribution.md) for enabling the channel.

## Advanced — for contributors / power users

- [desktop-build.md](desktop-build.md) — local build flow + Docker reproducible builder + per-OS deps + cross-compile matrix
- [desktop-architecture.md](desktop-architecture.md) — proxy-based AssetServer architecture
- [desktop-distribution.md](desktop-distribution.md) — release signing + Ed25519 keypair setup
- [desktop-qa.md](desktop-qa.md) — QA checklist for releases
