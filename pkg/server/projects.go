package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/arketov/iterion/pkg/assistantmission"
	"github.com/arketov/iterion/pkg/botregistry"
	"github.com/arketov/iterion/pkg/errtrack"
	"github.com/arketov/iterion/pkg/runtime"
	"github.com/arketov/iterion/pkg/runview"
	"github.com/arketov/iterion/pkg/runwatch"
	"github.com/arketov/iterion/pkg/secrets"
	"github.com/arketov/iterion/pkg/server/projects"
	"github.com/arketov/iterion/pkg/store"
)

// projectsEnabled reports whether project-registry endpoints make
// sense for this server's run mode. Cloud (multi-tenant) mode has no
// per-folder concept — the SPA short-circuits the switcher there.
func (s *Server) projectsEnabled() bool {
	return s.cfg.Mode != "cloud"
}

// registerInitialProject is called once at server boot to make sure
// the workdir the operator launched against is present in the shared
// registry (so it shows up in the switcher even when they've never
// used the desktop app). Mirrors AddProjectSilently in the desktop
// bindings: it does NOT trigger a hot-swap or broadcast.
func (s *Server) registerInitialProject() {
	if !s.projectsEnabled() || s.cfg.WorkDir == "" {
		return
	}
	if s.cfg.SkipProjectRegistration {
		return
	}
	abs, err := filepath.Abs(s.cfg.WorkDir)
	if err != nil {
		s.logger.Warn("projects: abs workdir %q: %v", s.cfg.WorkDir, err)
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.logger.Warn("projects: load registry: %v", err)
		return
	}
	cfg.AddOrTouch(abs)
	if err := cfg.Save(); err != nil {
		s.logger.Warn("projects: save registry: %v", err)
	}
	s.cacheCurrentProjectID(cfg.CurrentProjectID)
}

// cacheCurrentProjectID stashes the latest current_project_id so
// handleServerInfo can surface it without touching disk on every poll.
// Safe to call under any lock state — it acquires stateMu itself.
func (s *Server) cacheCurrentProjectID(id string) {
	s.stateMu.Lock()
	s.currentProjectID = id
	s.stateMu.Unlock()
}

// CurrentProjectID returns the cached id of the active registry entry.
// Empty when the registry has never been written or the server is in
// cloud mode.
func (s *Server) CurrentProjectID() string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.currentProjectID
}

// projectResponse is the wire shape returned to the SPA. Mirrors
// studio/src/api/projects.ts.
type projectResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	StoreDir   string `json:"store_dir,omitempty"`
	LastOpened string `json:"last_opened"`
	Color      string `json:"color,omitempty"`
}

func toProjectResponse(p projects.Project) projectResponse {
	return projectResponse{
		ID:         p.ID,
		Name:       p.Name,
		Dir:        p.Dir,
		StoreDir:   p.StoreDir,
		LastOpened: p.LastOpened.UTC().Format("2006-01-02T15:04:05.000Z"),
		Color:      p.Color,
	}
}

func toProjectResponses(ps []projects.Project) []projectResponse {
	out := make([]projectResponse, len(ps))
	for i, p := range ps {
		out[i] = toProjectResponse(p)
	}
	return out
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if !s.projectsEnabled() {
		s.writeJSONFor(w, r, []projectResponse{})
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "load projects: %v", err)
		return
	}
	s.writeJSONFor(w, r, toProjectResponses(cfg.RecentProjects))
}

func (s *Server) handleCurrentProject(w http.ResponseWriter, r *http.Request) {
	if !s.projectsEnabled() {
		s.writeJSONFor(w, r, nil)
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "load projects: %v", err)
		return
	}
	cur := cfg.Current()
	if cur == nil {
		s.writeJSONFor(w, r, nil)
		return
	}
	s.writeJSONFor(w, r, toProjectResponse(*cur))
}

type switchProjectRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleSwitchProject(w http.ResponseWriter, r *http.Request) {
	// CSRF: this POST swaps the studio's active workspace (filesystem reach).
	// A `text/plain` body is a CORS "simple request" (no preflight), so a page
	// the operator visits could POST here cross-origin; requireSafeOrigin
	// rejects a cross-origin browser Origin while passing same-origin SPA and
	// no-Origin CLI callers.
	if !s.requireSafeOrigin(w, r) {
		return
	}
	if !s.projectsEnabled() {
		s.httpErrorFor(w, r, http.StatusBadRequest, "projects switching is not available in cloud mode")
		return
	}
	var req switchProjectRequest
	if !decodeJSONCapped(s, w, r, &req, 64<<10) { // 64 KB cap; payload is a single id
		return
	}
	if req.ID == "" {
		s.httpErrorFor(w, r, http.StatusBadRequest, "id is required")
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "load projects: %v", err)
		return
	}
	target := cfg.ByID(req.ID)
	if target == nil {
		s.httpErrorFor(w, r, http.StatusNotFound, "project not found: %s", req.ID)
		return
	}
	if !s.servesDir(target.Dir) {
		s.refuseProjectMove(w, r)
		return
	}
	if !cfg.SetCurrent(req.ID) {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "set current: race")
		return
	}
	if err := cfg.Save(); err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "save projects: %v", err)
		return
	}
	s.cacheCurrentProjectID(cfg.CurrentProjectID)
	s.writeJSONFor(w, r, toProjectResponse(*target))
}

type addProjectRequest struct {
	Dir string `json:"dir"`
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	// CSRF guard (registers + switches to an operator-supplied directory) —
	// see handleSwitchProject.
	if !s.requireSafeOrigin(w, r) {
		return
	}
	if !s.projectsEnabled() {
		s.httpErrorFor(w, r, http.StatusBadRequest, "projects are not available in cloud mode")
		return
	}
	var req addProjectRequest
	if !decodeJSONCapped(s, w, r, &req, 64<<10) { // 64 KB cap; payload is a single directory path
		return
	}
	if req.Dir == "" {
		s.httpErrorFor(w, r, http.StatusBadRequest, "dir is required")
		return
	}
	abs, err := filepath.Abs(req.Dir)
	if err != nil {
		s.httpErrorFor(w, r, http.StatusBadRequest, "abs path: %v", err)
		return
	}
	if !s.servesDir(abs) {
		s.refuseProjectMove(w, r)
		return
	}
	// The served folder: register the spelling registerInitialProject uses,
	// so a different case or short name cannot duplicate the entry.
	served, err := filepath.Abs(s.currentWorkDir())
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "abs path: %v", err)
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "load projects: %v", err)
		return
	}
	p := cfg.AddOrTouch(served)
	if err := cfg.Save(); err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "save projects: %v", err)
		return
	}
	s.cacheCurrentProjectID(cfg.CurrentProjectID)
	s.writeJSONFor(w, r, toProjectResponse(p))
}

func (s *Server) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	// CSRF guard (mutates the project registry) — see handleSwitchProject.
	if !s.requireSafeOrigin(w, r) {
		return
	}
	if !s.projectsEnabled() {
		s.httpErrorFor(w, r, http.StatusBadRequest, "projects are not available in cloud mode")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.httpErrorFor(w, r, http.StatusBadRequest, "id is required")
		return
	}
	cfg, err := projects.Load()
	if err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "load projects: %v", err)
		return
	}
	target := cfg.ByID(id)
	if target == nil {
		s.httpErrorFor(w, r, http.StatusNotFound, "project not found: %s", id)
		return
	}
	if s.servesDir(target.Dir) {
		s.refuseProjectMove(w, r)
		return
	}
	// The registry's current pointer is shared by every process; moving it
	// must not move this server, which keeps serving its own folder.
	if !cfg.Remove(id) {
		s.httpErrorFor(w, r, http.StatusNotFound, "project not found: %s", id)
		return
	}
	if err := cfg.Save(); err != nil {
		s.httpErrorFor(w, r, http.StatusInternalServerError, "save projects: %v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hotSwapStopBudget bounds the previous service's background stop. The
// reconcile half awaits the scan in flight, and that scan is cancelled first,
// so the budget only covers a store call already under way — past it the
// switch proceeds and runview logs what it could not wait for.
const hotSwapStopBudget = 5 * time.Second

// swapWorkDir is the hot-swap primitive. Builds a fresh runview.Service
// and Watcher for the new directory, then atomically replaces the
// server's references under stateMu. In-flight engine goroutines from
// the previous project keep their captured *Service reference and
// drain to the old store in the background — the SPA's reset on
// `project_switched` makes that invisible to the user.
//
// The previous service's PERIODIC workers stop, though: they are the
// server's, not the runs'. Left alive they scan a store the server no longer
// serves — one more orphan-reconcile ticker, pipeline scheduler and stall
// poll per switch, for the process lifetime.
func (s *Server) swapWorkDir(ctx context.Context, newDir string) error {
	abs, err := filepath.Abs(newDir)
	if err != nil {
		return fmt.Errorf("abs: %w", err)
	}
	info, err := statDir(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", abs)
	}
	// The switch holds the bot tree's lock on the root it leaves until the
	// new root is in place: a writer holding it resolved and writes in that
	// root, and keeps it until it lets go (holdBotTreeWrites).
	releaseTree, err := s.holdBotTreeWrites(ctx, botTreeWriterProjectSwitch)
	if err != nil {
		return err
	}
	defer releaseTree()

	// Build the new run-console service first; if construction fails
	// we abort before touching any live state.
	storeDir := store.ResolveStoreDir(abs, "")
	// The new project's directories are the host's too: an error about them
	// must not name them (installPathRedaction; the old roots stay registered).
	installPathRedaction(abs, storeDir)

	// Rebuild the local secret store for the NEW project so its project layer
	// (<store-dir>/secrets.json) follows the switch; the global layer + master
	// key are unchanged. Best-effort: on failure keep the previous store (warn),
	// don't abort the switch. nil when local secrets aren't enabled.
	newLocalSecrets := s.localSecretStore()
	localSecretsEnabled := s.cfg.Mode != "cloud" && s.sealer != nil && newLocalSecrets != nil
	if localSecretsEnabled {
		if ls, err := secrets.LocalStoreForProject(storeDir); err != nil {
			s.logger.Warn("projects: rebuild local secret store for %q: %v — keeping previous", abs, err)
		} else {
			newLocalSecrets = ls
		}
	}

	var newRuns *runview.Service
	var newAssistantWatches runwatch.Store
	var newAssistantMissions assistantmission.Store
	if storeDir != "" {
		svcOpts := []runview.ServiceOption{
			runview.WithLogger(s.logger),
			runview.WithWorkDir(abs),
			runview.WithMaxConcurrentPipelines(s.cfg.MaxConcurrentPipelines),
			// Sandbox-by-default (same resolution as `iterion run`).
			runview.WithSandboxDefault(runtime.ResolveGlobalSandboxDefault(s.logger)),
		}
		if mcpStore, openErr := store.New(storeDir); openErr != nil {
			s.logger.Warn("projects: open runs MCP store for %q: %v — sandboxed runs.read disabled", abs, openErr)
		} else if opt, ok := s.boardMCPServiceOption(s.logger, mcpStore); ok {
			svcOpts = append(svcOpts, opt)
		}
		// Run-health alerting is a server-level setting, not a per-project
		// one: without this the first switch silently ends alerting for the
		// rest of the process (the boot service keeps the only manager, over
		// a store nobody serves).
		if s.cfg.Alerts != nil {
			svcOpts = append(svcOpts, runview.WithAlerts(*s.cfg.Alerts))
		}
		if localSecretsEnabled {
			svcOpts = append(svcOpts, runview.WithLocalSecrets(newLocalSecrets, s.sealer))
		}
		if r := s.botContextResolver(); r != nil {
			svcOpts = append(svcOpts, runview.WithBotContextResolver(r))
		}
		if s.usageCapSource != nil {
			svcOpts = append(svcOpts, runview.WithUsageCapSource(s.usageCapSource))
		}
		svcOpts = append(svcOpts, runview.WithRepositoryWrites(s.holdRunMergeRepository))
		svc, svcErr := runview.NewService(storeDir, svcOpts...)
		if svcErr != nil {
			return fmt.Errorf("runview service: %w", svcErr)
		}
		newRuns = svc
		if s.cfg.RunWatches == nil {
			newAssistantWatches = runwatch.NewFSStore(svc.RunStore().Root())
			if err := newAssistantWatches.EnsureSchema(context.Background()); err != nil {
				return fmt.Errorf("assistant run watch store: %w", err)
			}
		}
		if s.cfg.AssistantMissions == nil {
			newAssistantMissions = assistantmission.NewFSStore(svc.RunStore().Root())
			if err := newAssistantMissions.EnsureSchema(context.Background()); err != nil {
				return fmt.Errorf("assistant mission store: %w", err)
			}
		}
		if bus := s.eventsBus(); bus != nil {
			newRuns.SetEventPublisher(bus)
		}
	}

	// Build the new watcher. Best-effort: a NewWatcher failure is
	// downgraded to a warning (same as the boot path); the swap goes
	// through without file-event push.
	var newWatcher *Watcher
	if s.cfg.Mode != "cloud" {
		w, werr := NewWatcher(abs, s.hub, s.logger)
		if werr != nil {
			s.logger.Warn("projects: new watcher for %q: %v", abs, werr)
		} else {
			newWatcher = w
		}
	}

	// Swap under the write lock. Capture the previous watcher so we
	// can stop it after releasing the lock; the previous Service is
	// intentionally NOT drained — its captured engine goroutines
	// continue to write to their original store.
	s.stateMu.Lock()
	oldWatcher := s.watcher
	oldRuns := s.runs
	s.cfg.WorkDir = abs
	s.cfg.StoreDir = storeDir
	s.runs = newRuns
	if newAssistantWatches != nil {
		s.assistantWatches = newAssistantWatches
	}
	if newAssistantMissions != nil {
		s.assistantMissions = newAssistantMissions
	}
	s.watcher = newWatcher
	s.localSecrets = newLocalSecrets
	// The run set changes wholesale on a project switch — drop the
	// runs-stats memo so per-run cost from the previous project can't
	// linger (and the cache can't grow unbounded across switches).
	s.statsCache.clear()
	// Snapshotted under the lock: the restart below runs outside it and must
	// not read a field another switch could be writing. A switch that
	// supplied no new watch store keeps the one already installed.
	watchesForMissions := s.assistantWatches
	watchCoordinator := s.assistantWatch
	if watchCoordinator != nil {
		watchCoordinator.setRuntime(s.runs, s.assistantWatches, s.effectivePathsFor(abs))
	}
	s.stateMu.Unlock()
	releaseTree()
	watchCoordinator.nudge()
	var prevMissionDone <-chan struct{}
	if newAssistantMissions != nil {
		prevMissionDone = s.restartAssistantMissions(newRuns, watchesForMissions, newAssistantMissions)
	}

	// Re-point the concurrency gate's reservation source at the new run
	// service. Skipping this leaves the fresh Service ungated while the
	// PREVIOUS project's tickets are still counted against it.
	s.wirePipelineReservations(newRuns)

	if oldWatcher != nil {
		oldWatcher.Stop()
	}
	// Detached from the caller: the swap has already happened, so a requester
	// that hung up must not leave the previous project's workers running.
	// The previous mission sweep is joined within the same budget, so the
	// previous project's store is quiet when the switch returns.
	if (oldRuns != nil && oldRuns != newRuns) || prevMissionDone != nil {
		stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), hotSwapStopBudget)
		if oldRuns != nil && oldRuns != newRuns {
			oldRuns.StopBackground(stopCtx)
		}
		if prevMissionDone != nil {
			select {
			case <-prevMissionDone:
			case <-stopCtx.Done():
			}
		}
		cancelStop()
	}
	if newWatcher != nil {
		errtrack.Go("server.fileWatcher", newWatcher.Start)
	}
	s.logger.Info("projects: swapped workdir to %s", abs)
	return nil
}

// projectSwitchUnsupported is the error_code of a project-registry request
// that would move or orphan the folder this server serves.
const projectSwitchUnsupported = "project_switch_unsupported"

// servesDir reports whether dir is the folder this server serves. A studio
// server is bound to one project at boot — its board, dispatcher, triggers and
// coordinators are built from that folder and are not re-bound by
// swapWorkDir — so the project routes only accept requests about this folder.
// PathKey folds symlinks, Windows 8.3 names and case; an empty workdir serves
// nothing (PathKey("") would resolve to the process cwd).
func (s *Server) servesDir(dir string) bool {
	cur := s.currentWorkDir()
	if cur == "" {
		return false
	}
	return botregistry.PathKey(dir) == botregistry.PathKey(cur)
}

// refuseProjectMove answers a request that would change the served folder.
// It names no host path: the folder is the caller's own, the refusal is not
// a place to echo it.
func (s *Server) refuseProjectMove(w http.ResponseWriter, r *http.Request) {
	s.httpErrorCode(w, r, http.StatusConflict, projectSwitchUnsupported,
		"this studio serves one project folder; start another studio (iterion studio --dir <folder>) for another project, or use iterion studio --workspace or the desktop app")
}

// statDir returns the FileInfo for an absolute directory path,
// translating ENOENT and permission errors into user-readable
// messages. Used by the add/switch handlers to fail fast before any
// state mutation.
func statDir(abs string) (os.FileInfo, error) {
	st, statErr := os.Stat(abs)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("path does not exist: %s", abs)
		}
		if errors.Is(statErr, os.ErrPermission) {
			return nil, fmt.Errorf("permission denied: %s", abs)
		}
		return nil, fmt.Errorf("stat %s: %w", abs, statErr)
	}
	return st, nil
}
