package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/store"
)

// ManagerState describes where the Manager is in its lifecycle.
//
//	idle    — no dispatcher process; config may or may not be present
//	running — dispatcher active, dispatching candidates each tick
//	paused  — dispatcher active, dispatch suspended (Pause was called)
//	error   — last Start attempt failed; LastError carries the cause
type ManagerState string

const (
	ManagerStateIdle    ManagerState = "idle"
	ManagerStateRunning ManagerState = "running"
	ManagerStatePaused  ManagerState = "paused"
	ManagerStateError   ManagerState = "error"
)

// ManagerOptions configures NewManager.
type ManagerOptions struct {
	StoreDir    string // the iterion store dir (e.g. ./.iterion)
	NativeStore *native.Store
	Logger      *iterlog.Logger
	// DefaultBotsPaths seeds the dispatcher's bot registry when the
	// persisted config (dispatcher.json) does not set its own. Used
	// by the studio so the SPA's per-ticket bot picker and the
	// dispatcher's per-ticket bot resolution share the same view of
	// the host's bots without the operator having to configure them
	// twice.
	DefaultBotsPaths []string
	// ProjectDir is the project root a RELATIVE path in the saved config
	// (workflow, assignee_workflows, bots.paths, workspace.root, hook path) is
	// resolved against: the studio writes `./bots/x/main.bot` meaning the
	// project's bots, not a file next to dispatcher.json in the store. Empty
	// falls back to the folder of the saved config file.
	ProjectDir string
	// RunEnv is the immutable project environment inherited by dispatcher
	// hooks and every engine the manager creates.
	RunEnv []string
	// DefaultsFn builds a zero-config dispatcher Config from the
	// host environment (typically the project's own bots as the routable
	// assignees, see cli.BuildDefaultConfig). When set, the
	// Manager exposes POST /defaults/apply which saves the returned
	// config and starts the dispatcher in one call. nil = the
	// endpoint returns 501 Not Implemented.
	//
	// Injected from pkg/cli (where the embed.FS lives) so the
	// pkg/dispatcher package stays free of the templates dep.
	DefaultsFn func() (*Config, error)
	// MigrateConfig, when set, is called with the config loaded from disk at
	// start-up and may rewrite it in place (a preset an older version saved);
	// true means it changed and is written back. Injected from pkg/cli for
	// the same reason as DefaultsFn.
	MigrateConfig func(*Config) bool
}

// Manager owns the lifecycle of an in-process Dispatcher instance.
// It is the surface the studio talks to (HTTP) so an operator can
// configure, start, pause, and stop dispatching entirely from the SPA
// without dropping into a terminal.
//
// One Manager per studio server. Safe for concurrent use.
type Manager struct {
	storeDir         string
	configPath       string
	runtimePath      string
	nativeStore      *native.Store
	logger           *iterlog.Logger
	defaultBotsPaths []string
	projectDir       string
	runEnv           []string
	defaultsFn       func() (*Config, error)

	mu sync.Mutex
	// raw is the config as saved on disk: `$GITHUB_TOKEN` stays a reference,
	// relative paths stay relative, "none" stays "none". It is what GET /config
	// shows (secrets masked) and what is written back on every save.
	raw *Config
	// cfg is the copy the dispatcher runs: env expanded, paths resolved,
	// defaults applied (Config.Resolved). Never persisted.
	cfg       *Config
	state     ManagerState
	cur       *Dispatcher
	runner    ManagedRunner
	cancel    context.CancelCauseFunc
	lastErr   error
	startedAt time.Time
	// bootNote is set when ITERION_DISPATCHER_AUTOSTART=0 kept a saved intent
	// from being restored at boot; cleared by the first Start.
	bootNote string
}

// NewManager constructs a Manager and loads any persisted config from
// <store-dir>/dispatcher/dispatcher.json. The Manager starts in the
// idle state; call Start when ready.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.StoreDir == "" {
		return nil, errors.New("manager: store dir required")
	}
	if opts.NativeStore == nil {
		return nil, errors.New("manager: native store required")
	}
	if opts.Logger == nil {
		return nil, errors.New("manager: logger required")
	}
	m := &Manager{
		storeDir:         opts.StoreDir,
		configPath:       filepath.Join(opts.StoreDir, "dispatcher", "dispatcher.json"),
		runtimePath:      runtimeStatePath(opts.StoreDir),
		nativeStore:      opts.NativeStore,
		logger:           opts.Logger,
		defaultBotsPaths: append([]string(nil), opts.DefaultBotsPaths...),
		projectDir:       opts.ProjectDir,
		runEnv:           append([]string(nil), opts.RunEnv...),
		defaultsFn:       opts.DefaultsFn,
		state:            ManagerStateIdle,
	}
	if raw, err := readConfigJSON(m.configPath); err == nil {
		// A preset an older version saved is rewritten on the RAW config: it
		// carries `$TOKEN` references and relative paths as written, so
		// saving it back puts nothing in clear.
		migrated := opts.MigrateConfig != nil && opts.MigrateConfig(raw)
		// A saved sh hook with no `shell:` on a PowerShell host runs under sh
		// by a text heuristic: write down the shell it already runs under, so
		// the choice is visible in the settings form and a later change of the
		// heuristic or of the host default cannot flip it.
		pinned := PinHeuristicShells(raw)
		if migrated || len(pinned) > 0 {
			if err := saveConfigJSON(m.configPath, raw); err != nil {
				opts.Logger.Warn("manager: persist migrated config (%s): %v", m.configPath, err)
			} else {
				opts.Logger.Info("manager: migrated the saved dispatcher config (%s)", m.configPath)
				if len(pinned) > 0 {
					opts.Logger.Info("manager: hooks %v are written for sh and were already running under it: saved `shell:` to say so; "+
						"edit or remove it to change that", pinned)
				}
			}
		}
		if rt, rerr := m.resolve(raw); rerr == nil {
			m.raw, m.cfg = raw, rt
		} else {
			opts.Logger.Warn("manager: load persisted config (%s): %v", m.configPath, rerr)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		opts.Logger.Warn("manager: load persisted config (%s): %v", m.configPath, err)
	}
	// Sweep stale claims left over from a dead local PID at server
	// boot, before any operator has clicked Start on the dispatcher.
	// Without this, /board would show "claimed by rog-<dead-pid>"
	// labels for the entire window between backend restart and the
	// operator manually starting the dispatcher (ticket 7221c7be).
	m.sweepStaleLocalClaimsAtBoot()
	m.pruneGhostRunsAtBoot()

	// Restore the operator's last-known intent. On first boot (no
	// runtime.json), default to Running when a config exists so the
	// SPA's "dispatcher: active" chip matches reality without the
	// operator having to click Start every session. Honour
	// ITERION_DISPATCHER_AUTOSTART=0 for CI environments that need to
	// start idle.
	persisted, err := loadDesiredState(m.runtimePath)
	if err != nil {
		opts.Logger.Warn("manager: load runtime state (%s): %v", m.runtimePath, err)
	}
	intent := resolveBootIntent(persisted, m.cfg != nil)
	if note := autoStartSuppressedNote(persisted); note != "" {
		m.bootNote = note
		opts.Logger.Info("manager: %s", note)
	}
	switch intent {
	case DesiredRunning, DesiredPaused:
		// Pause BEFORE the actor's first tick when restoring a paused
		// session. Start-then-Pause raced the immediate boot tick and
		// minted sibling runs for in_progress tickets whose last_run
		// was still paused_waiting_human.
		if startErr := m.start(intent == DesiredPaused); startErr != nil {
			opts.Logger.Warn("manager: auto-start declined: %v", startErr)
		}
	}
	return m, nil
}

// sweepStaleLocalClaimsAtBoot runs Adapter.SweepStaleClaims directly
// against the native store so the board is clean from the first
// /api/native/issues request, regardless of whether the dispatcher
// has been started yet. Best-effort: any error is logged at warn.
// Same isStale predicate as the dispatcher's start-time sweep.
func (m *Manager) sweepStaleLocalClaimsAtBoot() {
	adapter := native.NewAdapter(m.nativeStore)
	host, _ := osHostname()
	if host == "" {
		host = "dispatcher"
	}
	cleared, err := adapter.SweepStaleClaims(func(marker string) bool {
		return isStaleLocalMarker(marker, host)
	})
	if err != nil {
		m.logger.Warn("manager: boot-time stale-claim sweep failed: %v", err)
		return
	}
	if len(cleared) > 0 {
		m.logger.Info("manager: released %d stale claim(s) at boot from dead local PIDs: %v", len(cleared), cleared)
	}
}

// ghostRunGrace is how young a run-history entry must be to be left alone by
// the boot-time ghost cleanup: a run stamped moments ago may not have reached
// the disk yet.
const ghostRunGrace = 10 * time.Minute

// pruneGhostRunsAtBoot drops from each card's run history the runs that have
// no directory under <store>/runs — the phantoms a failed after_create hook
// used to leave behind (a console that 404s, a diff of nothing). Best-effort:
// an error is logged at warn and never blocks boot. A missing runs/ directory
// is not evidence that every run is gone (the store may simply be fresh or
// elsewhere), but the dispatcher launches into this same store, so a card's
// run lives here or nowhere; the grace period and the claimed-card skip cover
// the in-flight cases.
func (m *Manager) pruneGhostRunsAtBoot() {
	if m.nativeStore == nil || m.storeDir == "" {
		return
	}
	runsDir := filepath.Join(m.storeDir, "runs")
	pruned, err := m.nativeStore.PruneRunRefs(func(runID string) bool {
		_, statErr := os.Stat(filepath.Join(runsDir, runID))
		// Only a definite "does not exist" counts as gone.
		return statErr == nil || !errors.Is(statErr, fs.ErrNotExist)
	}, ghostRunGrace)
	if err != nil {
		m.logger.Warn("manager: boot-time ghost-run cleanup failed: %v", err)
		return
	}
	if len(pruned) > 0 {
		total := 0
		for _, p := range pruned {
			total += len(p.RunIDs)
		}
		m.logger.Info("manager: removed %d run(s) that never started from the history of %d card(s)", total, len(pruned))
	}
}

// ManagerStatus is the snapshot the SPA reads via GET /status.
type ManagerStatus struct {
	State     ManagerState `json:"state"`
	HasConfig bool         `json:"has_config"`
	StartedAt *time.Time   `json:"started_at,omitempty"`
	LastError string       `json:"last_error,omitempty"`
	// BootNote says why the dispatcher was not restored at boot
	// (ITERION_DISPATCHER_AUTOSTART=0); absent otherwise. The studio shows it
	// while the dispatcher is stopped.
	BootNote string `json:"boot_note,omitempty"`
}

// Status returns the current lifecycle state and any last error.
func (m *Manager) Status() ManagerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := ManagerStatus{
		State:     m.state,
		HasConfig: m.cfg != nil,
		LastError: errString(m.lastErr),
		BootNote:  m.bootNote,
	}
	if !m.startedAt.IsZero() {
		t := m.startedAt
		s.StartedAt = &t
	}
	return s
}

// Config returns the currently-active configuration (or nil if none
// has been written yet).
func (m *Manager) Config() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// SaveConfig validates and persists a new configuration. If a
// dispatcher is running, it is reloaded with the new settings; if it's
// idle, the config is saved for the next Start.
//
// cfg is taken as the form to SAVE and is not modified: `$GITHUB_TOKEN` stays a
// reference and relative paths stay relative on disk, while the dispatcher
// runs the resolved copy (env expanded, paths made absolute against the
// project root, defaults applied). Writing the resolved copy used to put
// tokens in clear into dispatcher.json.
func (m *Manager) SaveConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("manager: config is nil")
	}
	raw, err := cfg.clone()
	if err != nil {
		return fmt.Errorf("manager: copy config: %w", err)
	}
	// A token the settings form got back masked ("***") is not a new value:
	// keep the one on file.
	m.mu.Lock()
	prev := m.raw
	m.mu.Unlock()
	carrySecrets(raw, prev)
	rt, err := m.resolve(raw)
	if err != nil {
		return err
	}
	// Serialise the disk write + in-memory swap under m.mu so two
	// concurrent SaveConfig calls don't end with disk reflecting one
	// race winner and m.cfg the other (F-CD-8). The actor's Reload is
	// fired after the lock release because Reload itself locks; we
	// snapshot the running dispatcher pointer under the lock so a
	// concurrent Stop+Start doesn't make us call Reload on a freshly-
	// killed actor.
	m.mu.Lock()
	if err := saveConfigJSON(m.configPath, raw); err != nil {
		m.mu.Unlock()
		return err
	}
	m.raw, m.cfg = raw, rt
	cur := m.cur
	m.mu.Unlock()
	if cur != nil {
		cur.Reload(rt)
	}
	return nil
}

// baseDir is what a relative path of the saved config resolves against: the
// project root, or — with no project known — the folder of the saved file.
func (m *Manager) baseDir() string {
	if m.projectDir != "" {
		return m.projectDir
	}
	return filepath.Dir(m.configPath)
}

// resolve builds the runtime copy of a raw config (Config.Resolved against the
// project root, the studio's default bot roots filling an empty bots.paths).
func (m *Manager) resolve(raw *Config) (*Config, error) {
	raw.SourcePath = m.configPath
	rt, err := raw.Resolved(m.baseDir(), m.defaultBotsPaths)
	if err != nil {
		return nil, err
	}
	m.warnUnsetTokenRefs(raw, rt)
	return rt, nil
}

// warnUnsetTokenRefs says so when a tracker token saved as an `$ENV` reference
// expands to nothing in this process: the config keeps the reference, never the
// token, so a studio started without the variable runs the tracker with no
// credential — silently, unless this is said.
func (m *Manager) warnUnsetTokenRefs(raw, rt *Config) {
	if m.logger == nil {
		return
	}
	check := func(kind, refTok, runTok string) {
		if refTok != "" && refTok != maskedToken && runTok == "" {
			m.logger.Warn("manager: %s tracker token %q expands to nothing: the variable is not set in this process, so the tracker runs without a credential", kind, refTok)
		}
	}
	if raw.Tracker.GitHub != nil && rt.Tracker.GitHub != nil {
		check("github", raw.Tracker.GitHub.Token, rt.Tracker.GitHub.Token)
	}
	if raw.Tracker.Forgejo != nil && rt.Tracker.Forgejo != nil {
		check("forgejo", raw.Tracker.Forgejo.Token, rt.Tracker.Forgejo.Token)
	}
	if raw.Tracker.GitLab != nil && rt.Tracker.GitLab != nil {
		check("gitlab", raw.Tracker.GitLab.Token, rt.Tracker.GitLab.Token)
	}
}

// RawConfig returns the saved configuration as written — `$ENV` references and
// relative paths intact, defaults not applied — or nil when none is saved. Its
// token fields hold real secrets or references: mask them with redactedConfig
// before it leaves the process.
func (m *Manager) RawConfig() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.raw
}

// ErrAlreadyRunning is returned by Start when a Dispatcher is already
// live — which NewManager's auto-start makes the NORMAL case for any
// caller starting explicitly on a store with a persisted config (e.g.
// `iterion dispatch` re-run on the same store dir). Callers that just
// need "a dispatcher is running with my config" should treat it as
// success: SaveConfig live-reloads the running instance.
var ErrAlreadyRunning = errors.New("manager: already running")

// Start spins up a fresh Dispatcher from the persisted config. Returns
// an error and stays in StateError when the start sequence fails (bad
// workflow, missing tracker creds, port conflict, …).
func (m *Manager) Start() error {
	return m.start(false)
}

// start is Start with an optional already-paused actor. paused=true
// sets the flag BEFORE the actor loop so the immediate first tick
// cannot dispatch (or mint a sibling) before Pause() would have run.
func (m *Manager) start(paused bool) error {
	m.mu.Lock()
	if m.cur != nil {
		m.mu.Unlock()
		return ErrAlreadyRunning
	}
	cfg := m.cfg
	m.mu.Unlock()
	if cfg == nil {
		return errors.New("manager: no config — save one first")
	}

	// NewRoutingRunner returns a plain EngineRunner when
	// cfg.AssigneeWorkflows is empty (backward-compatible) or a
	// RoutingRunner that dispatches per assignee with cfg.Workflow as
	// the fallback.
	runner, err := NewRoutingRunner(cfg, m.logger, WithProjectRunEnv(m.runEnv))
	if err != nil {
		m.setError(err)
		return err
	}
	trk, err := buildTracker(cfg, m.nativeStore)
	if err != nil {
		_ = runner.Close()
		m.setError(err)
		return err
	}
	wsRoot := cfg.Workspace.Root
	if wsRoot == "" {
		wsRoot = filepath.Join(m.storeDir, "dispatcher", "workspaces")
	}
	workspaces, err := NewWorkspaces(wsRoot)
	if err != nil {
		_ = runner.Close()
		m.setError(err)
		return err
	}
	c, err := New(Options{
		Config:     cfg,
		Tracker:    trk,
		Runner:     runner,
		Workspaces: workspaces,
		Logger:     m.logger,
		StoreDir:   m.storeDir,
		BaseEnv:    m.runEnv,
	})
	if err != nil {
		_ = runner.Close()
		m.setError(err)
		return err
	}

	// WithCancelCause: teardown cancels with ErrRunInterrupted so in-flight
	// runs persist failed_resumable even when this parent dies BEFORE the
	// per-run Cancel fires — a context records only its FIRST cause, so a
	// late r.Cancel(ErrRunInterrupted) on an already-cancelled parent would
	// be a no-op and the engine would write `cancelled` (operator-owned,
	// never auto-resumed) for a stop no operator asked.
	ctx, cancel := context.WithCancelCause(context.Background())
	if paused {
		c.paused.Store(true)
	}
	c.Start(ctx)

	m.mu.Lock()
	m.cur = c
	m.runner = runner
	m.cancel = cancel
	if paused {
		m.state = ManagerStatePaused
	} else {
		m.state = ManagerStateRunning
	}
	m.lastErr = nil
	m.bootNote = ""
	m.startedAt = time.Now().UTC()
	m.mu.Unlock()
	if paused {
		m.persistDesired(DesiredPaused)
		m.logger.Info("manager: dispatcher started paused (workflow=%s, tracker=%s)", workflowLabel(cfg), trk.Name())
	} else {
		m.persistDesired(DesiredRunning)
		m.logger.Info("manager: dispatcher started (workflow=%s, tracker=%s)", workflowLabel(cfg), trk.Name())
	}
	return nil
}

// Stop tears down the active Dispatcher AND records `desired=stopped`
// on disk so a future cold boot stays idle. Use from the HTTP
// /dispatcher/stop handler — it captures operator intent. No-op when
// idle.
func (m *Manager) Stop() {
	m.teardown()
	m.persistDesired(DesiredStopped)
}

// Shutdown tears down the active Dispatcher WITHOUT mutating the
// persisted desired state. Use from the studio server's graceful-
// shutdown path so a SIGTERM / Ctrl-C doesn't overwrite the operator's
// previous "running" or "paused" intent — the next boot replays what
// was on disk before shutdown. No-op when idle.
//
// Without this split a `task studio:dev` watchexec rebuild would
// silently flip the operator's session back to idle on every Go-file
// edit (Stop fires in the shutdown path, persistDesired wins over the
// running-or-paused state the operator last asked for).
func (m *Manager) Shutdown() {
	m.teardown()
}

// teardown is the shared body of Stop/Shutdown. Stops the actor,
// cancels its context, closes the runner. Idempotent.
func (m *Manager) teardown() {
	m.mu.Lock()
	cur, runner, cancel := m.cur, m.runner, m.cancel
	m.cur, m.runner, m.cancel = nil, nil, nil
	m.state = ManagerStateIdle
	m.startedAt = time.Time{}
	m.mu.Unlock()
	if cur != nil {
		cur.Stop()
	}
	if cancel != nil {
		// Internal stop — the interrupt cause makes any run this parent
		// tears down persist failed_resumable, whatever the ordering with
		// the per-run Cancel.
		cancel(runtime.ErrRunInterrupted)
	}
	if runner != nil {
		if err := runner.Close(); err != nil {
			m.logger.Warn("manager: runner close: %v", err)
		}
	}
}

// Pause suspends new dispatches on the active Dispatcher. Runs in flight
// continue. Returns an error when no dispatcher is running.
func (m *Manager) Pause() error {
	m.mu.Lock()
	cur := m.cur
	if cur == nil {
		m.mu.Unlock()
		return errors.New("manager: not running")
	}
	m.state = ManagerStatePaused
	m.mu.Unlock()
	cur.Pause()
	m.persistDesired(DesiredPaused)
	return nil
}

// Resume undoes Pause.
func (m *Manager) Resume() error {
	m.mu.Lock()
	cur := m.cur
	if cur == nil {
		m.mu.Unlock()
		return errors.New("manager: not running")
	}
	m.state = ManagerStateRunning
	m.mu.Unlock()
	cur.Resume()
	m.persistDesired(DesiredRunning)
	return nil
}

// persistDesired writes the operator's last lifecycle intent so the
// next cold boot of the studio replays it. Failures are logged but do
// not abort the lifecycle transition — a missing/unwritable runtime
// state file degrades back to the pre-fix behaviour (the operator
// clicks Start once after every restart), not to a broken dispatcher.
func (m *Manager) persistDesired(d DesiredState) {
	if m.runtimePath == "" {
		return
	}
	if err := saveDesiredState(m.runtimePath, d); err != nil {
		m.logger.Warn("manager: persist runtime state (%s, desired=%s): %v", m.runtimePath, d, err)
	}
}

// Current returns the active Dispatcher (or nil when idle). Callers
// must not retain the pointer past the next Stop.
func (m *Manager) Current() *Dispatcher {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

// Refresh asks the active dispatcher to run a poll tick immediately. No-op
// when the dispatcher is stopped. It satisfies trigger.Nudger so a board
// trigger can promote a card (stamp its bot) and have the dispatcher pick it
// up now instead of at the next 30s poll — the Claim stays the sole launch
// authority, so this only collapses latency, it never races the poll.
func (m *Manager) Refresh() {
	if cur := m.Current(); cur != nil {
		cur.Refresh()
	}
}

// CancelRun signals the active Dispatcher to cancel a run by its RunID.
// Returns true when a matching in-flight run was found. Falsey when the
// dispatcher is idle or the runID is unknown to it — typical when the
// runID belongs to a manual studio launch (handled by the runview
// Manager) or is already terminal.
func (m *Manager) CancelRun(runID string) bool {
	cur := m.Current()
	if cur == nil {
		return false
	}
	return cur.CancelByRunID(runID)
}

// TransitionMergedIssue moves the issue identified by issueID to the
// state configured under Agent.MergedState. Best-effort:
//   - returns nil silently when the dispatcher is idle, the config
//     leaves MergedState empty, or the value is "none". The merge
//     handler treats these as "feature opt-out", not failure.
//   - returns a wrapped error for tracker-level failures so the
//     caller can log them; the merge itself remains successful
//     regardless.
//
// Used by the server's merge handler to drive the GitHub-style
// "close issue on PR merge" UX for native and remote trackers.
func (m *Manager) TransitionMergedIssue(ctx context.Context, issueID string) error {
	if issueID == "" {
		return nil
	}
	target, tr := m.mergedTransition()
	if tr == nil || target == "" || target == "none" {
		return nil
	}
	if err := tr.UpdateState(ctx, issueID, target); err != nil {
		return fmt.Errorf("transition merged issue %s → %s: %w", issueID, target, err)
	}
	m.logger.Info("dispatcher: merged-issue transition %s → %s", issueID, target)
	return nil
}

// mergedTransition resolves the merged-state target and the tracker to
// apply it with, working whether or not the polling actor is live.
//
// When the actor is running its tracker is authoritative (handles
// native + external trackers). When idle — e.g. a watchexec restart
// hasn't re-spawned the actor yet, or the operator paused/stopped
// dispatching — we fall back to the native store directly: the board
// is a filesystem store that's always writable, so a studio-driven
// merge must still close the ticket regardless of the actor's
// lifecycle. Without this fallback the review→done transition was
// silently dropped whenever the merge click landed in an actor-down
// window.
func (m *Manager) mergedTransition() (string, tracker.Tracker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		cfg := m.cur.cfg.Load()
		if cfg == nil {
			return "", nil
		}
		return cfg.Agent.MergedState, m.cur.tracker
	}
	if m.nativeStore == nil {
		return "", nil
	}
	// m.cfg was normalized by loadConfigJSON's applyDefaults: unset →
	// "done", "none" → "" (opt-out). Honor it verbatim — re-defaulting
	// "" back to "done" here would resurrect a transition the operator
	// deliberately disabled. Only when no config was ever persisted do
	// we fall back to the package default.
	target := DefaultMergedState
	if m.cfg != nil {
		target = m.cfg.Agent.MergedState
	}
	return target, native.NewAdapter(m.nativeStore)
}

func (m *Manager) setError(err error) {
	m.mu.Lock()
	m.state = ManagerStateError
	m.lastErr = err
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// tracker factory
// ---------------------------------------------------------------------------

// buildTracker constructs a tracker.Tracker matching cfg.Tracker.Kind.
// Used by both the studio's Manager and the standalone `iterion
// dispatch` CLI so the wiring stays in one place. The GitHub + Forgejo
// factories live alongside in external.go; they are in-package and
// called directly (no init-time indirection — there was never an
// external consumer of the override hook).
func buildTracker(cfg *Config, ns *native.Store) (tracker.Tracker, error) {
	switch cfg.Tracker.Kind {
	case TrackerKindNative:
		return native.NewAdapter(ns), nil
	case TrackerKindGitHub:
		return buildGitHubTrackerFromConfig(cfg.Tracker.GitHub)
	case TrackerKindForgejo:
		return buildForgejoTrackerFromConfig(cfg.Tracker.Forgejo)
	case TrackerKindGitLab:
		return buildGitLabTrackerFromConfig(cfg.Tracker.GitLab)
	default:
		return nil, fmt.Errorf("dispatcher: unsupported tracker kind %q", cfg.Tracker.Kind)
	}
}

// ---------------------------------------------------------------------------
// config persistence
// ---------------------------------------------------------------------------

// readConfigJSON parses the saved config exactly as written: nothing expanded,
// no default applied, nothing validated (see Config.Resolved).
func readConfigJSON(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config json: %w", err)
	}
	cfg.SourcePath = path
	return &cfg, nil
}

// loadConfigJSON reads the saved config and returns the resolved, validated
// copy, relative paths taken against the file's own folder. The Manager does
// the two steps itself (it resolves against the project root); this is the
// one-shot form.
func loadConfigJSON(path string) (*Config, error) {
	raw, err := readConfigJSON(path)
	if err != nil {
		return nil, err
	}
	return raw.Resolved(filepath.Dir(path), nil)
}

// maskedToken is what GET /config shows in place of a literal secret, and what
// PUT /config reads back as "unchanged".
const maskedToken = "***"

// envRefRe matches a token that is only an environment reference
// ($NAME / ${NAME}): not a secret, shown as written.
var envRefRe = regexp.MustCompile(`^\$\{?[A-Za-z_][A-Za-z0-9_]*\}?$`)

// maskToken hides a literal token and leaves an env reference visible.
func maskToken(tok string) string {
	if tok == "" || envRefRe.MatchString(tok) {
		return tok
	}
	return maskedToken
}

// carrySecrets replaces a masked tracker token in in with the one prev holds.
// The settings form gets tokens back masked and sends them back as it got
// them; without this a plain "Save" would overwrite a real token with "***".
// With nothing to carry the masked value is cleared rather than stored.
func carrySecrets(in, prev *Config) {
	carry := func(tok *string, old string) {
		if *tok == maskedToken {
			*tok = old
		}
	}
	oldGH, oldFJ, oldGL := "", "", ""
	if prev != nil {
		if prev.Tracker.GitHub != nil {
			oldGH = prev.Tracker.GitHub.Token
		}
		if prev.Tracker.Forgejo != nil {
			oldFJ = prev.Tracker.Forgejo.Token
		}
		if prev.Tracker.GitLab != nil {
			oldGL = prev.Tracker.GitLab.Token
		}
	}
	if in.Tracker.GitHub != nil {
		carry(&in.Tracker.GitHub.Token, oldGH)
	}
	if in.Tracker.Forgejo != nil {
		carry(&in.Tracker.Forgejo.Token, oldFJ)
	}
	if in.Tracker.GitLab != nil {
		carry(&in.Tracker.GitLab.Token, oldGL)
	}
}

// patchHookFields rewrites only the named fields of the named hooks
// (hook name → field → value, e.g. after_create → script, shell) in the config
// file at path and leaves every other field exactly as it was written. A
// migration must not re-save the whole loaded Config: loading expands `$ENV`
// tokens and absolutises paths, and writing that back would put a token on
// disk in clear. The write is atomic and keeps the file private (0600).
func patchHookFields(path string, patches map[string]map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		return errors.New("config has no hooks object")
	}
	for name, fields := range patches {
		hook, _ := hooks[name].(map[string]any)
		if hook == nil {
			return fmt.Errorf("config has no %s hook", name)
		}
		for field, value := range fields {
			if value == "" {
				continue
			}
			hook[field] = value
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(path, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func saveConfigJSON(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config json: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config json: marshal: %w", err)
	}
	// 0600: the file may hold a token the operator typed in.
	if err := store.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("config json: write: %w", err)
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// RegisterRoutes wires the Manager's REST surface onto mux. It owns
// every /api/v1/dispatcher/* endpoint: lifecycle (start/pause/resume/
// stop/status), config (GET/PUT), and the operational endpoints
// (state/refresh/issues/.../cancel/ws) which delegate to the active
// Dispatcher when one is running.
func (m *Manager) RegisterRoutes(mux *http.ServeMux, prefix string) {
	m.RegisterRoutesWithMiddleware(mux, prefix, nil)
}

// RegisterRoutesWithMiddleware mounts the routes through a caller-
// supplied wrapper (typically the studio server's requireAuth) so
// every operator endpoint is gated when the server binds non-loopback.
// nil falls back to the identity wrap (same as RegisterRoutes).
func (m *Manager) RegisterRoutesWithMiddleware(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	p := strings.TrimSuffix(prefix, "/")
	if wrap == nil {
		wrap = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET "+p+"/status", wrap(http.HandlerFunc(m.handleStatus)))
	mux.Handle("GET "+p+"/config", wrap(http.HandlerFunc(m.handleGetConfig)))
	mux.Handle("PUT "+p+"/config", wrap(http.HandlerFunc(m.handlePutConfig)))
	mux.Handle("POST "+p+"/start", wrap(http.HandlerFunc(m.handleStart)))
	mux.Handle("POST "+p+"/stop", wrap(http.HandlerFunc(m.handleStop)))
	mux.Handle("POST "+p+"/pause", wrap(http.HandlerFunc(m.handlePause)))
	mux.Handle("POST "+p+"/resume", wrap(http.HandlerFunc(m.handleResume)))
	mux.Handle("POST "+p+"/defaults/apply", wrap(http.HandlerFunc(m.handleApplyDefaults)))

	mux.Handle("GET "+p+"/state", wrap(http.HandlerFunc(m.handleSnapshot)))
	mux.Handle("POST "+p+"/refresh", wrap(http.HandlerFunc(m.handleRefresh)))
	mux.Handle("POST "+p+"/reload", wrap(http.HandlerFunc(m.handleReload)))
	mux.Handle("GET "+p+"/issues/{id}", wrap(http.HandlerFunc(m.handleIssueDetail)))
	mux.Handle("POST "+p+"/issues/{id}/cancel", wrap(http.HandlerFunc(m.handleIssueCancel)))
	mux.Handle("POST "+p+"/retries/{id}/run-now", wrap(http.HandlerFunc(m.handleRetryNow)))
	mux.Handle("DELETE "+p+"/retries/{id}", wrap(http.HandlerFunc(m.handleRetryDrop)))
	mux.Handle("GET "+p+"/ws", wrap(http.HandlerFunc(m.handleWS)))
}

func (m *Manager) handleStatus(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	// The saved config as written (env references, relative paths), not the
	// resolved copy the dispatcher runs: the settings form edits what is on
	// disk and sends it back, so it must see exactly that.
	cfg := m.RawConfig()
	if cfg == nil {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no config persisted yet"})
		return
	}
	WriteJSON(w, http.StatusOK, redactedConfig(cfg))
}

// redactedConfig returns a deep-ish copy of cfg with secret values
// (tracker tokens) masked. Without this masking, GET /config
// surfaces the raw OAuth/PAT tokens to anyone who can reach the
// management port. A token that is only an env reference ($GITHUB_TOKEN)
// is not a secret and stays visible; PUT /config reads a masked value back
// as "unchanged" (carrySecrets).
func redactedConfig(cfg *Config) *Config {
	out := *cfg
	if out.Tracker.GitHub != nil {
		gh := *out.Tracker.GitHub
		gh.Token = maskToken(gh.Token)
		out.Tracker.GitHub = &gh
	}
	if out.Tracker.Forgejo != nil {
		fj := *out.Tracker.Forgejo
		fj.Token = maskToken(fj.Token)
		out.Tracker.Forgejo = &fj
	}
	if out.Tracker.GitLab != nil {
		gl := *out.Tracker.GitLab
		gl.Token = maskToken(gl.Token)
		out.Tracker.GitLab = &gl
	}
	return &out
}

func (m *Manager) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	// Cap the body: a dispatcher config is small, and an unbounded Decode lets
	// a client stream arbitrary memory into the process.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		WriteErr(w, http.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if err := m.SaveConfig(&cfg); err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	WriteJSON(w, http.StatusOK, redactedConfig(m.RawConfig()))
}

func (m *Manager) handleStart(w http.ResponseWriter, _ *http.Request) {
	if err := m.Start(); err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, m.Status())
}

// handleApplyDefaults builds the host's default Config via the
// injected DefaultsFn, persists it, and starts the dispatcher. The
// endpoint is the studio's "auto-configure & start" one-click path;
// the bot also calls it (via the future dispatcher.control capability)
// after moving tickets to `ready` when no config exists yet.
//
// Returns 501 if no DefaultsFn was injected (out-of-process or test
// builds), 409 if a config already exists (callers pass ?replace=1 to reset
// it to the defaults, or PUT to edit it — applying defaults over an existing
// config would silently clobber operator edits), 400 if the defaults builder fails
// (typically a binary built before the templates were embedded),
// 202 + ManagerStatus on success. `?replace=1` lifts the 409: the operator
// asked to return the saved config to the defaults.
func (m *Manager) handleApplyDefaults(w http.ResponseWriter, r *http.Request) {
	if m.defaultsFn == nil {
		WriteErr(w, http.StatusNotImplemented, errors.New("dispatcher defaults: not wired in this build"))
		return
	}
	// ?replace=1 is the operator's explicit "back to the default settings":
	// the saved config is overwritten (SaveConfig live-reloads a running
	// dispatcher). Without it an existing config is never clobbered.
	replace := r.URL.Query().Get("replace") == "1"
	if m.Config() != nil && !replace {
		WriteErr(w, http.StatusConflict, errors.New("dispatcher defaults: a config already exists — POST /defaults/apply?replace=1 replaces it with the default settings (the studio's \"Reset to default settings\" button in Dispatcher settings does exactly that), or PUT /config edits it"))
		return
	}
	cfg, err := m.defaultsFn()
	if err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if replace && m.Current() != nil {
		// A running dispatcher keeps the tracker, workflow and repository it
		// was started with (SaveConfig live-reloads settings only): tear it
		// down so the Start below builds it again from the defaults.
		m.teardown()
	}
	if err := m.SaveConfig(cfg); err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	if err := m.Start(); err != nil && !errors.Is(err, ErrAlreadyRunning) {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, m.Status())
}

func (m *Manager) handleStop(w http.ResponseWriter, _ *http.Request) {
	m.Stop()
	WriteJSON(w, http.StatusAccepted, m.Status())
}

func (m *Manager) handlePause(w http.ResponseWriter, _ *http.Request) {
	if err := m.Pause(); err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, m.Status())
}

func (m *Manager) handleResume(w http.ResponseWriter, _ *http.Request) {
	if err := m.Resume(); err != nil {
		WriteErr(w, http.StatusBadRequest, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, m.Status())
}

// Operational endpoints — delegate to the active dispatcher.

func (m *Manager) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	cur := m.Current()
	if cur == nil {
		// Idle: return a stub snapshot so the SPA always has a
		// rendered table (empty running/retries) without 404s.
		WriteJSON(w, http.StatusOK, Snapshot{
			GeneratedAt: time.Now().UTC(),
		})
		return
	}
	WriteJSON(w, http.StatusOK, cur.Snapshot())
}

func (m *Manager) handleRefresh(w http.ResponseWriter, _ *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: not running"))
		return
	}
	cur.Refresh()
	WriteJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (m *Manager) handleReload(w http.ResponseWriter, _ *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: not running"))
		return
	}
	cfg := m.Config()
	if cfg == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: no config"))
		return
	}
	cur.Reload(cfg)
	WriteJSON(w, http.StatusOK, m.Status())
}

func (m *Manager) handleIssueDetail(w http.ResponseWriter, r *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusNotFound, errors.New("dispatcher not running"))
		return
	}
	id := r.PathValue("id")
	snap := cur.Snapshot()
	for _, row := range snap.Running {
		if row.IssueID == id {
			WriteJSON(w, http.StatusOK, row)
			return
		}
	}
	for _, row := range snap.Retries {
		if row.IssueID == id {
			WriteJSON(w, http.StatusOK, row)
			return
		}
	}
	WriteErr(w, http.StatusNotFound, errors.New("issue not tracked by dispatcher"))
}

func (m *Manager) handleIssueCancel(w http.ResponseWriter, r *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: not running"))
		return
	}
	id := r.PathValue("id")
	cur.Cancel(id)
	WriteJSON(w, http.StatusAccepted, map[string]string{"issue_id": id, "status": "cancel_requested"})
}

// handleRetryNow / handleRetryDrop are the retry queue's two row actions (see
// Dispatcher.RetryNow, Dispatcher.GiveUpRetry).
func (m *Manager) handleRetryNow(w http.ResponseWriter, r *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: not running"))
		return
	}
	cur.serveRetryNow(w, r)
}

func (m *Manager) handleRetryDrop(w http.ResponseWriter, r *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusBadRequest, errors.New("manager: not running"))
		return
	}
	cur.serveRetryDrop(w, r)
}

func (m *Manager) handleWS(w http.ResponseWriter, r *http.Request) {
	cur := m.Current()
	if cur == nil {
		WriteErr(w, http.StatusServiceUnavailable, fmt.Errorf("dispatcher not running"))
		return
	}
	// Delegate to the dispatcher's existing WS handler, which knows
	// how to upgrade + attach to its bridge.
	cur.handleWS(w, r)
}
