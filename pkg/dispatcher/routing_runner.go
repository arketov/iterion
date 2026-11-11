package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/arketov/iterion/pkg/botregistry"
	iterlog "github.com/arketov/iterion/pkg/log"
)

// RoutingRunner dispatches workflow runs based on the issue's
// `Assignee` field. Each registered assignee maps to a pre-compiled
// Runner; issues whose assignee is empty or unknown fall back to the
// default Runner.
//
// This is how the dispatcher "auto-pilots" a kanban populated by
// whats-next.bot (or any other producer): the producer stamps each
// issue with `--assignee <bot_name>`, then a single
// `iterion.dispatcher.yaml` declares the mapping:
//
//	workflow: workflows/default.bot                    # fallback
//	assignee_workflows:
//	  feature_dev:        bots/feature_dev/main.bot
//	  whole_improve_loop: bots/whole_improve_loop/main.bot
//	  secured-renovacy:   bots/secured-renovacy/main.bot
//
// Beyond the static map, when BotsPaths is configured the runner ALSO
// resolves an assignee against the discovered bot catalog on demand:
// any ENABLED bot is routable by its technical name without an explicit
// assignee_workflows entry. This is what lets a custom bot Nexie just
// created auto-run end-to-end. Dynamically-resolved workflows are
// compiled lazily on first dispatch and cached. Disabled bots are NOT
// auto-routed (the catalog toggle gates autonomous use).
type RoutingRunner struct {
	// Default is invoked when the issue's assignee is empty or resolves
	// to no route. MUST be non-nil.
	Default Runner

	// ByAssignee maps assignee names to per-assignee Runners, pre-compiled
	// from cfg.AssigneeWorkflows. Read-only after construction.
	ByAssignee map[string]Runner

	// BotsPaths are the registry discovery roots for dynamic resolution.
	// Empty disables the dynamic fallback (static map + default only).
	BotsPaths []string

	logger        *iterlog.Logger
	engineOptions []EngineRunnerOption

	// compile builds a Runner for a resolved workflow path. Injectable so
	// tests can avoid real IR compilation; defaults to NewEngineRunner.
	compile func(path string) (Runner, error)

	// listBots reads the bot catalogue. Injectable so tests can observe the
	// walks; defaults to botregistry.List.
	listBots func(botregistry.ListOptions) ([]botregistry.Entry, error)

	mu     sync.Mutex
	dynamic map[string]Runner // normalized bot name → lazily-compiled runner
}

// Dispatch implements Runner.
func (r *RoutingRunner) Dispatch(ctx context.Context, spec DispatchSpec) error {
	if r == nil {
		return errors.New("routing runner: nil runner")
	}
	if rn, ok := r.routeFor(spec.Assignee); ok {
		return rn.Dispatch(ctx, spec)
	}
	if r.Default == nil {
		// No fallback workflow is configured (require_assignee presets run only
		// the project's own bots): a card no bot routes to has nothing to run.
		// The dispatcher refuses such a card before it claims it; this is the
		// backstop for any path that reaches the runner anyway.
		return fmt.Errorf("%w (assignee %q)", ErrNoBotRoute, spec.Assignee)
	}
	return r.Default.Dispatch(ctx, spec)
}

// ErrNoBotRoute is returned by RoutingRunner.Dispatch when the card's
// assignee routes to no bot and no default workflow is configured.
var ErrNoBotRoute = errors.New("dispatcher: no bot routes this card and no default workflow is configured")

// staticRoute resolves the pre-compiled per-assignee Runner for a key.
// Exact match wins; otherwise a normalized comparison tolerates
// kebab/snake/case differences (lookupBotKey). Returns (nil,false) on no
// static route.
func (r *RoutingRunner) staticRoute(key string) (Runner, bool) {
	if r == nil {
		return nil, false
	}
	rn, ok := lookupBotKey(r.ByAssignee, key)
	if !ok || rn == nil {
		return nil, false
	}
	return rn, true
}

// routeFor resolves the Runner for a routing key: the static map first,
// then (when BotsPaths is set) a dynamic registry resolution that
// compiles and caches the resolved bot's workflow. Returns (nil,false)
// when nothing routes — the caller falls back to Default.
func (r *RoutingRunner) routeFor(key string) (Runner, bool) {
	if rn, ok := r.staticRoute(key); ok {
		return rn, true
	}
	path, ok := r.resolveDynamicPath(key)
	if !ok {
		return nil, false
	}
	return r.dynamicRunner(key, path)
}

// resolveDynamicPath finds an ENABLED bot's workflow path via the
// registry. Returns ("",false) when dynamic routing is off, the bot is
// unknown, or it is disabled in the catalog (disabled = not for
// autonomous dispatch). Does NOT compile — cheap enough for the actor's
// pre-claim HasRoute guard.
//
// Enablement is the resolved value: the manifest `enabled` default
// composed with the workspace overlay (.iterion/bot-overrides.yaml). The
// workspace root is recovered from the discovery paths
// (WorkdirFromPaths), so a bot disabled OR re-enabled from the studio
// Catalog manager is honoured at dispatch time, matching what Nexie sees.
func (r *RoutingRunner) resolveDynamicPath(key string) (string, bool) {
	e, ok := r.resolveDynamicEntry(key)
	if !ok {
		return "", false
	}
	return e.MainFile(), true
}

// BotDispatchVars returns the dispatch_vars the manifest of the ENABLED
// discovered bot that key routes to declares, read from the catalogue NOW —
// a bot created or edited after the dispatcher started is picked up on its next
// card, with no dispatcher.json to regenerate. ok is false when key routes
// through a static assignee_workflows entry (that workflow is not the bot the
// registry would name), or to no enabled discovered bot, or the bot declares no
// dispatch_vars.
func (r *RoutingRunner) BotDispatchVars(key string) (map[string]string, bool) {
	if _, static := r.staticRoute(key); static {
		return nil, false
	}
	e, ok := r.resolveDynamicEntry(key)
	if !ok || len(e.DispatchVars) == 0 {
		return nil, false
	}
	return e.DispatchVars, true
}

// resolveDynamicEntry is the registry lookup behind resolveDynamicPath: the
// ENABLED discovered bot key names (tolerantly: botregistry.NormalizeName),
// or false when dynamic routing is off, the bot is unknown or disabled.
func (r *RoutingRunner) resolveDynamicEntry(key string) (botregistry.Entry, bool) {
	e, found := r.lookupDynamicEntry(key)
	if !found || !e.Enabled {
		return botregistry.Entry{}, false // unknown, or disabled in the catalog → not auto-routed
	}
	return e, true
}

// lookupDynamicEntry reads the catalogue ONCE and returns the discovered bot
// key names, enabled or not; found is false when dynamic routing is off, the
// catalogue is unreadable or no bot has that name. The route decision and the
// reason a card is refused are both taken from this one walk.
func (r *RoutingRunner) lookupDynamicEntry(key string) (botregistry.Entry, bool) {
	if r == nil || len(r.BotsPaths) == 0 || key == "" {
		return botregistry.Entry{}, false
	}
	list := r.listBots
	if list == nil {
		list = botregistry.List
	}
	entries, err := list(botregistry.ListOptions{
		Paths:   r.BotsPaths,
		Workdir: botregistry.WorkdirFromPaths(r.BotsPaths),
	})
	if err != nil {
		// No catalogue, no dynamic route — a workspace overlay its reader
		// refused included (*botregistry.OverlayRefusal): the operator's
		// visibility decisions are unread, and a bot is routed only on
		// them. Said, since nothing else here is.
		if r.logger != nil {
			r.logger.Warn("routing runner: no dynamic route for %q: the bot catalogue is not read: %v", key, err)
		}
		return botregistry.Entry{}, false
	}
	nk := botregistry.NormalizeName(key)
	for _, e := range entries {
		if botregistry.NormalizeName(e.Name) == nk {
			return e, true
		}
	}
	return botregistry.Entry{}, false
}

// dynamicRunner returns the cached runner for a resolved bot, compiling
// + caching it on first use. Compilation can be expensive (full IR
// compile), so this is only reached from the worker (Dispatch) and the
// post-claim buildSpec path, never the pre-claim HasRoute guard.
func (r *RoutingRunner) dynamicRunner(key, path string) (Runner, bool) {
	nk := botregistry.NormalizeName(key)
	r.mu.Lock()
	defer r.mu.Unlock()
	if rn, ok := r.dynamic[nk]; ok && rn != nil {
		return rn, true
	}
	mk := r.compile
	if mk == nil {
		mk = func(p string) (Runner, error) { return NewEngineRunner(p, r.logger, r.engineOptions...) }
	}
	rn, err := mk(path)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("routing runner: compile dynamic bot %q (%s): %v", key, path, err)
		}
		return nil, false
	}
	if r.dynamic == nil {
		r.dynamic = make(map[string]Runner)
	}
	r.dynamic[nk] = rn
	return rn, true
}

// HasRoute reports whether key resolves to a dedicated route (static map
// or a dynamically-resolvable ENABLED bot) rather than falling through
// to Default. Compile-free so the dispatcher's pre-claim honest-fail
// guard stays fast.
func (r *RoutingRunner) HasRoute(key string) bool {
	if _, ok := r.staticRoute(key); ok {
		return true
	}
	_, ok := r.resolveDynamicPath(key)
	return ok
}

// RouteState is HasRoute plus, for a key that does not route, whether the
// catalogue names a bot of that spelling that is switched off (disabled): one
// catalogue read for both answers, so refusing a card with no bot does not walk
// the bot folders a second time to pick its reason. Compile-free like HasRoute.
func (r *RoutingRunner) RouteState(key string) (routed, disabled bool) {
	if _, ok := r.staticRoute(key); ok {
		return true, false
	}
	e, found := r.lookupDynamicEntry(key)
	if !found {
		return false, false
	}
	return e.Enabled, !e.Enabled
}

// DeclaredVars returns the declared var-name set of the workflow `key`
// routes to (static or dynamically-compiled), else Default's. Returns
// nil when the resolved runner can't report its vars.
func (r *RoutingRunner) DeclaredVars(key string) map[string]struct{} {
	runner := r.Default
	if rn, ok := r.routeFor(key); ok {
		runner = rn
	}
	vd, ok := runner.(interface {
		DeclaredVars(string) map[string]struct{}
	})
	if !ok {
		return nil
	}
	return vd.DeclaredVars(key)
}

// Close releases all per-assignee runners, the dynamically-compiled
// runners, and the default, in any order. A runner that does not
// implement `Close() error` is skipped silently. The first error is
// returned but cleanup continues so a partial failure does not leak.
func (r *RoutingRunner) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	closeOne := func(name string, runner Runner) {
		if runner == nil {
			return
		}
		closer, ok := runner.(interface{ Close() error })
		if !ok {
			return
		}
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("routing runner: close %q: %w", name, err)
		}
	}
	for name, runner := range r.ByAssignee {
		closeOne(name, runner)
	}
	r.mu.Lock()
	for name, runner := range r.dynamic {
		closeOne(name, runner)
	}
	r.mu.Unlock()
	closeOne("default", r.Default)
	return firstErr
}

// NewRoutingRunner builds a RoutingRunner from the dispatcher config's
// Workflow (default), AssigneeWorkflows (static routes), and Bots.Paths
// (dynamic registry resolution). Returns the underlying default
// EngineRunner directly only when there are neither static routes NOR
// dynamic discovery roots (no need to pay the wrapper cost). The caller
// is responsible for calling Close on the returned ManagedRunner.
func NewRoutingRunner(cfg *Config, logger *iterlog.Logger, opts ...EngineRunnerOption) (ManagedRunner, error) {
	if cfg == nil {
		return nil, errors.New("routing runner: config required")
	}
	// The default (fallback) workflow is optional: a config with
	// require_assignee runs only bots it can route to. Then Default stays nil
	// and a card no bot routes to is refused (ErrNoBotRoute), never run.
	var defaultRunner ManagedRunner
	if cfg.Workflow != "" {
		var err error
		defaultRunner, err = NewEngineRunner(cfg.Workflow, logger, opts...)
		if err != nil {
			return nil, fmt.Errorf("routing runner: default workflow %s: %w", cfg.Workflow, err)
		}
		if len(cfg.AssigneeWorkflows) == 0 && len(cfg.Bots.Paths) == 0 {
			return defaultRunner, nil
		}
	} else if len(cfg.AssigneeWorkflows) == 0 && len(cfg.Bots.Paths) == 0 {
		return nil, errors.New("routing runner: nothing to run: set cfg.Workflow, assignee_workflows or bots.paths")
	}
	r := &RoutingRunner{
		Default:       defaultRunner,
		ByAssignee:    make(map[string]Runner, len(cfg.AssigneeWorkflows)),
		BotsPaths:     cfg.Bots.Paths,
		logger:        logger,
		engineOptions: append([]EngineRunnerOption(nil), opts...),
	}
	for assignee, wfPath := range cfg.AssigneeWorkflows {
		if assignee == "" {
			return nil, errors.New("routing runner: assignee_workflows contains an empty key")
		}
		runner, err := NewEngineRunner(wfPath, logger, opts...)
		if err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("routing runner: assignee %q workflow %s: %w", assignee, wfPath, err)
		}
		r.ByAssignee[assignee] = runner
	}
	return r, nil
}
