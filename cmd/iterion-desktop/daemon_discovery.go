//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/arketov/iterion/pkg/store"
)

// daemonInfo is the on-disk shape of a per-project daemon's discovery
// file. Each `iterion-desktop --server-only --project=<dir>` writes one
// of these to ~/.iterion/daemons/<project-key>.json so the GUI can find
// the daemon for the project the user wants to open without scanning
// every running process.
//
// The legacy global ~/.iterion/desktop.json is still written for
// backward compatibility with the iterion-control MCP, but it points at
// the most-recently-launched daemon — multi-project discovery uses the
// per-project files instead.
type daemonInfo struct {
	URL        string `json:"url"`
	PID        int    `json:"pid"`
	ProjectDir string `json:"project_dir"`
	StartedAt  string `json:"started_at"`
}

const daemonRegistryDirName = "daemons"

// daemonRegistryDir is ~/.iterion/daemons. Per-project files land here.
func daemonRegistryDir() string {
	base := desktopDataDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, daemonRegistryDirName)
}

// daemonInfoPath returns the absolute path to a project's daemon.json.
// Uses encodeProjectDirKey so the same project always lands at the same
// file (matching the convention pkg/store uses for runs/storedir).
func daemonInfoPath(projectDir string) string {
	dir := daemonRegistryDir()
	if dir == "" || projectDir == "" {
		return ""
	}
	return filepath.Join(dir, encodeProjectDirKey(projectDir)+".json")
}

// writeDaemonInfo atomically updates the per-project daemon.json. Called
// by the daemon on startup; the partner removeDaemonInfo runs on signal
// shutdown so a freshly-killed daemon doesn't leave a stale pointer.
// The registry is the only way the GUI finds a daemon: one it cannot find
// still holds the project's lock, so every open of the project spawns a
// daemon that dies on that lock. The caller treats an error as fatal.
func writeDaemonInfo(projectDir, url string) error {
	target := daemonInfoPath(projectDir)
	if target == "" {
		return fmt.Errorf("daemon registry: no data directory resolved for %s", projectDir)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("daemon registry: %w", err)
	}
	info := daemonInfo{
		URL:        url,
		PID:        os.Getpid(),
		ProjectDir: projectDir,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("daemon registry: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("daemon registry: %w", err)
	}
	// The GUI polls this file while it waits for the daemon, and on
	// Windows a rename over a file another process holds open (a reader,
	// an antivirus scan) fails for as long as it is held: retry for up to
	// 2s before calling it fatal.
	var renameErr error
	for attempt := 0; attempt < 40; attempt++ {
		if renameErr = os.Rename(tmp, target); renameErr == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return fmt.Errorf("daemon registry: %w", renameErr)
}

// removeDaemonInfo clears this daemon's per-project daemon.json on shutdown,
// leaving a record another daemon wrote there (removeOwnDiscoveryFile).
// runHeadless runs it before it releases the project lock.
func removeDaemonInfo(projectDir string) error {
	target := daemonInfoPath(projectDir)
	if target == "" {
		return nil
	}
	if err := removeOwnDiscoveryFile(target); err != nil {
		return fmt.Errorf("daemon registry: %w", err)
	}
	return nil
}

// removeOwnDiscoveryFile deletes a discovery file — a daemon's registry
// entry or desktop.json — only while the record in it still names this
// process. Another server may have written its own record there since: a
// daemon of another project into desktop.json, which no lock guards, or a
// daemon of the same project whose lock lives in another config dir.
// Deleting that record would hide a live server. A missing file, an
// unparseable one or a record of another pid is left alone, without error.
//
// On Windows a reader holding the file open (the GUI polling the registry,
// the iterion-control MCP, a script, an antivirus scan) makes the delete
// fail with a sharing violation for as long as it holds it: Go opens files
// without FILE_SHARE_DELETE. So the read, check and delete are retried for
// up to 2s like the rename in writeDaemonInfo; each attempt re-reads, so a
// record rewritten meanwhile is still spared. Returns the last error.
func removeOwnDiscoveryFile(path string) error {
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		if attempt > 0 {
			time.Sleep(50 * time.Millisecond)
		}
		var raw []byte
		if raw, err = os.ReadFile(path); err == nil {
			var rec struct {
				PID int `json:"pid"`
			}
			if json.Unmarshal(raw, &rec) != nil || rec.PID != os.Getpid() {
				return nil
			}
			err = os.Remove(path)
		}
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return err
}

// findDaemonForProject returns the live daemon URL for the given project
// directory, or ("", false) if none is found OR the recorded daemon is
// dead (pid gone or port not accepting). Stale entries are silently
// pruned so the GUI doesn't attach to a corpse.
func findDaemonForProject(projectDir string) (string, bool) {
	target := daemonInfoPath(projectDir)
	if target == "" {
		return "", false
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return "", false
	}
	var info daemonInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		removeDaemonFileIfUnchanged(target, raw)
		return "", false
	}
	if !daemonAlive(info.PID, info.URL) {
		removeDaemonFileIfUnchanged(target, raw)
		return "", false
	}
	return info.URL, true
}

// removeDaemonFileIfUnchanged prunes a stale discovery file, but only if its
// content is still exactly what the caller read and judged dead. A new daemon
// may have overwritten the file with its own record between the read and the
// removal; deleting that would hide a live daemon (which holds the project
// lock) from the GUI until restart. The re-read narrows the window to the
// gap between it and the Remove, which is orders of magnitude smaller than
// the liveness probe the caller just ran.
func removeDaemonFileIfUnchanged(path string, seen []byte) {
	cur, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(cur, seen) {
		return
	}
	_ = os.Remove(path)
}

// listLiveDaemons returns every daemon currently registered with a live
// pid + accepting port. Used by the quit popup to surface the list of
// projects whose daemons would survive a GUI close, so the operator
// can decide whether to stop them or keep them running.
func listLiveDaemons() []daemonInfo {
	dir := daemonRegistryDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]daemonInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info daemonInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			continue
		}
		if !daemonAlive(info.PID, info.URL) {
			removeDaemonFileIfUnchanged(filepath.Join(dir, e.Name()), raw)
			continue
		}
		out = append(out, info)
	}
	return out
}

// daemonAlive combines a pid-liveness probe (processAlive, per platform)
// with a TCP dial against the daemon's port. Both must succeed: pid alone
// allows a crashed daemon to look alive, port alone could let us attach
// to a totally unrelated process that grabbed the same port after a crash.
func daemonAlive(pid int, url string) bool {
	if pid <= 0 || url == "" {
		return false
	}
	if !processAlive(pid) {
		return false
	}
	hostPort := strings.TrimRight(
		strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://"),
		"/",
	)
	conn, err := net.DialTimeout("tcp", hostPort, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// activeRunsOnDaemon probes a daemon's HTTP surface for in-flight runs
// and returns the count of entries with status=="running". Returns -1
// on any probe failure so callers fall back to the safe path (don't
// auto-stop a daemon we can't introspect).
//
// We count by `status` rather than the daemon's `Active` flag because
// `Active` only flags runs the daemon's own runview.Manager is driving
// — i.e. runs the daemon spawned. CLI-launched runs (`iterion run …`
// against the same store-dir) write status=running but never appear
// in the manager's handle map, so an `Active`-only filter undercounts
// them and the close-confirmation dialog gets skipped: the operator
// closes the GUI window, the daemon SIGTERMs silently, the CLI run
// keeps progressing on its own subprocess but loses HTTP visibility
// (no live log stream, no runs list refresh) until the daemon comes
// back. Counting by status preserves the dialog for both flavours;
// the dialog message already speaks to "background daemons" rather
// than the runs themselves.
//
// Timeout is intentionally tight: this blocks the GUI shutdown path.
func activeRunsOnDaemon(url string) int {
	if url == "" {
		return -1
	}
	base := strings.TrimRight(url, "/")
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(base + "/api/runs?status=running")
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return -1
	}
	var body struct {
		Runs []struct {
			Status string `json:"status"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return -1
	}
	n := 0
	for _, r := range body.Runs {
		if r.Status == "running" {
			n++
		}
	}
	return n
}

// totalActiveRunsAcrossDaemons sums activeRunsOnDaemon for every entry. As
// soon as a single probe fails (returns -1), the function bails with -1 so
// onBeforeClose falls back to the confirmation dialog rather than silently
// killing a daemon we can't verify.
func totalActiveRunsAcrossDaemons(daemons []daemonInfo) int {
	total := 0
	for _, d := range daemons {
		n := activeRunsOnDaemon(d.URL)
		if n < 0 {
			return -1
		}
		total += n
	}
	return total
}

// projectKeyForStorePath extracts the encoded project key from an iterion
// store path. Returns ("", false) when the path is the global ~/.iterion
// slot (no project dimension) or otherwise can't be parsed.
//
// Store paths come from `pkg/server/runs_global.go` and have one of two
// shapes (mirroring globalStoreRoots()):
//
//	$HOME/.iterion                          → ("", false)  // global slot
//	$HOME/.iterion/projects/<encoded-key>   → (<encoded-key>, true)
//
// Anything else returns false too, so callers can safely fall back to
// "use the current daemon" for unrecognised shapes.
func projectKeyForStorePath(storePath string) (string, bool) {
	if storePath == "" {
		return "", false
	}
	cleaned := filepath.Clean(storePath)
	parent := filepath.Base(filepath.Dir(cleaned))
	if parent != "projects" {
		return "", false
	}
	key := filepath.Base(cleaned)
	if !strings.HasPrefix(key, "-") {
		return "", false
	}
	return key, true
}

// projectDirFromKey reverses encodeProjectDirKey. It's a best-effort
// decode used to give spawnDaemonForProject something to launch when we
// don't already have a live daemon for the project. The encoding is
// lossy in principle (a literal "-" in the original path becomes "-",
// indistinguishable from a separator), so the result is only a hint.
//
// On Windows a key like "-C--Users-me-proj" is resolved against the disk
// (a dash may be a separator or part of a folder name); the hint is returned
// only when exactly one existing directory matches, otherwise "" so the
// caller stays on its current daemon instead of spawning one for a
// nonexistent directory.
//
// A key ending in "~<16 hex>" (a Windows path with a literal dash, see
// store.EncodeWorkDirKey) is decoded from its stem and accepted only when the
// decoded directory encodes back to exactly that key; otherwise a sibling
// directory with the same stem would be returned.
func projectDirFromKey(key string) string {
	if runtime.GOOS == "windows" {
		return decodeHashedWindowsProjectKey(key, dirExists)
	}
	return decodeUnixProjectKey(key)
}

// decodeHashedWindowsProjectKey is decodeWindowsProjectKey that also
// understands the "~<16 hex>" suffix: it strips the suffix, decodes the stem
// and verifies the result against the full key.
func decodeHashedWindowsProjectKey(key string, isDir func(string) bool) string {
	i := len(key) - 17
	if i < 0 || key[i] != '~' || !isLowerHex(key[i+1:]) {
		return decodeWindowsProjectKey(key, isDir)
	}
	got := decodeWindowsProjectKey(key[:i], isDir)
	if got == "" || store.EncodeWorkDirKey(got) != key {
		return ""
	}
	return got
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func decodeUnixProjectKey(key string) string {
	if !strings.HasPrefix(key, "-") {
		return ""
	}
	return "/" + strings.ReplaceAll(strings.TrimPrefix(key, "-"), "-", "/")
}

// maxKeyProbes caps the filesystem probes one key decode may spend, so a
// long key with many dashes cannot make the decoder explode.
const maxKeyProbes = 4096

// decodeWindowsProjectKey decodes "-<drive>--<a>-<b>" into a directory under
// `<drive>:\` by walking the filesystem: every "-" is either a path
// separator or a literal dash in a folder name, and only the disk can tell
// which. It returns the single existing directory the key can denote, or ""
// when there is none, more than one, or the key has another shape (UNC path,
// no drive letter).
func decodeWindowsProjectKey(key string, isDir func(string) bool) string {
	if len(key) < 4 || key[0] != '-' || key[2] != '-' || key[3] != '-' {
		return ""
	}
	drive := key[1]
	if !(drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z') {
		return ""
	}
	rest := key[4:]
	if rest == "" {
		return ""
	}
	return resolveKeyPath(string(drive)+`:\`, rest, isDir)
}

// resolveKeyPath resolves the dash-encoded relative path rest under root,
// trying at each step every way of grouping the next dash-separated tokens
// into one folder name that exists. Returns the path only if exactly one
// complete interpretation exists on disk.
func resolveKeyPath(root, rest string, isDir func(string) bool) string {
	budget := maxKeyProbes
	exhausted := false
	var found []string
	var walk func(cur string, tokens []string)
	walk = func(cur string, tokens []string) {
		if len(found) > 1 {
			return
		}
		if len(tokens) == 0 {
			found = append(found, cur)
			return
		}
		name := ""
		for j := 1; j <= len(tokens); j++ {
			if j == 1 {
				name = tokens[0]
			} else {
				name += "-" + tokens[j-1]
			}
			if budget <= 0 {
				// Not every interpretation was examined, so a lone hit
				// cannot be called unique.
				exhausted = true
				return
			}
			budget--
			if name == "" || name == "." || name == ".." {
				continue
			}
			cand := filepath.Join(cur, name)
			if isDir(cand) {
				walk(cand, tokens[j:])
				if len(found) > 1 {
					return
				}
			}
		}
	}
	walk(root, strings.Split(rest, "-"))
	if exhausted || len(found) != 1 {
		return ""
	}
	return found[0]
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// findDaemonForStorePath returns the live daemon URL serving the given
// iterion store path, or ("", false) when:
//   - the path is the global ~/.iterion slot (no per-project daemon
//     concept applies; the caller should use its current daemon URL)
//   - the per-project daemon discovery file is missing
//   - the recorded daemon is dead (pid gone or port not accepting)
//
// A stale discovery entry is pruned in-place: the file read by key, or the
// one findDaemonForProject reads on the fallback path.
func findDaemonForStorePath(storePath string) (string, bool) {
	key, ok := projectKeyForStorePath(storePath)
	if !ok {
		return "", false
	}
	// The daemon.json filename is encodeProjectDirKey(projectDir)+".json",
	// and `key` is exactly that encoded stem (same encoding as
	// pkg/store/storedir). So read the discovery file directly by key and
	// trust its recorded ProjectDir, rather than lossily decoding the key
	// back to a path — projectDirFromKey can't recover an original "-" in
	// the project dir, which would re-encode to the wrong filename and
	// miss a live daemon.
	dir := daemonRegistryDir()
	if dir == "" {
		return "", false
	}
	path := filepath.Join(dir, key+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		// No discovery file for this key — fall back to the lossy decode
		// as a best-effort hint (covers legacy files that predate the
		// recorded project_dir field).
		if hint := projectDirFromKey(key); hint != "" {
			return findDaemonForProject(hint)
		}
		return "", false
	}
	var info daemonInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		removeDaemonFileIfUnchanged(path, raw)
		return "", false
	}
	// Judge the record just read. Re-deriving the filename from its
	// project_dir (findDaemonForProject) would read, and could prune, a
	// different file when the recorded project_dir encodes to another key.
	if daemonAlive(info.PID, info.URL) {
		return info.URL, true
	}
	removeDaemonFileIfUnchanged(path, raw)
	return "", false
}

// encodeProjectDirKey is the project key of store.EncodeWorkDirKey, so the
// discovery file, the lock and the log file are named after the same key as
// the project's store directory.
//
//	/home/user/work/<project>  ->  -home-user-work-<project>
//	C:\users\u\<project>       ->  -C--users-u-<project>
func encodeProjectDirKey(absPath string) string {
	return store.EncodeWorkDirKey(absPath)
}
