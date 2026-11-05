package native

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/arketov/iterion/internal/fswatch"
)

// newFSWatcher is the seam through which a test can make the host refuse
// a watch (the ENOSPC/EMFILE a loaded CI runner really returns). Atomic
// like every seam in this package — see fireSeam.
var newFSWatcher atomic.Pointer[func() (*fsnotify.Watcher, error)]

// fsWatcherCtor returns fswatch's constructor — whose refusal carries the
// resource evidence store.go logs — unless a test installed one. The Add
// below goes through fswatch.Add for the same evidence: a spent
// max_user_watches budget is reported by inotify_add_watch, not by the
// constructor's inotify_init1 (#1554).
func fsWatcherCtor() func() (*fsnotify.Watcher, error) {
	if f := newFSWatcher.Load(); f != nil {
		return *f
	}
	return fswatch.NewWatcher
}

// indexWatcher watches <root>/issues/ for filesystem changes made by
// out-of-process writers (typically the `iterion __mcp-board` stdio
// MCP subprocess that whats-next's emit_action spawns) and refreshes
// the parent Store's in-memory index so subsequent List / Get calls
// see those writes.
//
// Without the watcher the index is populated exactly once at NewStore
// and only mutates through the parent process's own mutators — an
// MCP subprocess writing issues/<new>.json lands on disk but stays
// invisible to /api/v1/native/issues until the daemon restarts. The
// previous workaround (read the MCP tool's response JSON directly
// inside the runtime) hid the issue from the operator's perspective
// inside one run, but every other observer (studio /board view,
// dispatcher poller) still saw the stale state.
//
// Reload strategy is intentionally simple: any Create / Write /
// Rename / Remove event under issues/ re-reads the affected file under
// the store's mutex and indexes it, or drops it when the file is really
// gone (the op is not trusted — see applyEvent). A periodic full
// reconcile (verifyInterval) backs the events up, because a watch loses
// events on every host.
// The daemon's own writes also fire fsnotify; the resulting re-read
// is wasteful but idempotent — much simpler than tracking "expected"
// vs "unexpected" events. With the current sub-1k-issue working set
// the redundant disk reads are noise; if a board ever grows past
// that, a "skip if matches just-written hash" optimisation can
// fit on top of this without changing the interface.
type indexWatcher struct {
	w         *fsnotify.Watcher
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// startIndexWatcher launches a goroutine that mirrors issues/ disk
// changes into s.index. Returns nil + no error when fsnotify is
// unavailable on the host (e.g. a read-only or kernel-restricted
// environment); the Store still works, it just can't see out-of-
// process writes — same as before this watcher existed.
func startIndexWatcher(s *Store) (*indexWatcher, error) {
	w, err := fsWatcherCtor()()
	if err != nil {
		return nil, err
	}
	issuesPath := filepath.Join(s.root, issuesDir)
	if err := fswatch.Add(w, issuesPath); err != nil {
		_ = w.Close()
		// Missing directory shouldn't be fatal: NewStore already
		// ensured it exists, but a hostile filesystem (read-only
		// bind, container mounts) can still reject inotify_add_watch.
		// Surface the error so the caller decides whether to abort.
		return nil, err
	}
	iw := &indexWatcher{
		w:    w,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	// Published on the store BEFORE the loop starts: the loop may report
	// the watch lost within its first tick, and watchLost only acts on the
	// watcher the store holds.
	s.mu.Lock()
	s.watcher = iw
	s.mu.Unlock()
	go iw.loop(s, issuesPath)
	return iw, nil
}

// Close stops the watcher goroutine and releases the fsnotify
// resources. Safe to call multiple times.
func (iw *indexWatcher) Close() error {
	if iw == nil {
		return nil
	}
	// sync.Once makes Close idempotent AND concurrency-safe: the prior
	// select/default idiom let two concurrent callers both reach the
	// default branch and double-close iw.stop (panic).
	iw.closeOnce.Do(func() {
		close(iw.stop)
		<-iw.done
		iw.closeErr = iw.w.Close()
	})
	return iw.closeErr
}

// defaultWatchCheckInterval is how often the loop asks fsnotify whether
// its watch still exists. The kernel drops an inotify watch when the
// watched directory is removed, renamed or unmounted (IN_IGNORED), and
// fsnotify then forgets it WITHOUT closing a channel or sending an error —
// the loop would sit forever on a watch that no longer exists. The
// question costs one mutex and a map lookup.
const defaultWatchCheckInterval = 5 * time.Second

// watchCheckIntervalOverride lets a test tighten the check. Atomic like
// every seam in this package — and this is the one the race detector
// catches first, since loop() reads it on the watcher's own goroutine.
var watchCheckIntervalOverride atomic.Pointer[time.Duration]

func watchCheckInterval() time.Duration {
	if d := watchCheckIntervalOverride.Load(); d != nil {
		return *d
	}
	return defaultWatchCheckInterval
}

// lost is called when the loop has established that its watch is gone:
// fsnotify forgot it (the directory went away), or a channel closed
// without a Close pending — Close closes the stop channel BEFORE it closes
// the fsnotify watcher, so a closed channel with no stop pending is not a
// Close. Hand the store over to the fallback net.
func (iw *indexWatcher) lost(s *Store) {
	select {
	case <-iw.stop:
		return
	default:
	}
	s.watchLost(iw)
}

func (iw *indexWatcher) loop(s *Store, issuesPath string) {
	defer close(iw.done)
	var mu sync.Mutex // guards seenErr only — store mutex covers index access.
	var seenErr bool
	check := time.NewTicker(watchCheckInterval())
	defer check.Stop()
	misses := 0 // consecutive empty watch lists: two before the watch is declared lost
	// The verification net behind an armed watch: a full reconcile every
	// verifyInterval, off this goroutine (rebuildAsync) so the event channel
	// keeps draining. A nil channel never fires — the net is off.
	var verifyC <-chan time.Time
	if d := verifyInterval(); d > 0 {
		verify := time.NewTicker(d)
		defer verify.Stop()
		verifyC = verify.C
	}
	for {
		select {
		case <-iw.stop:
			return
		case <-verifyC:
			s.rebuildAsync("periodic verification")
		case <-check.C:
			// Two consecutive empty answers before the hand-over: on Linux
			// the list empties only when the kernel dropped the watch, and
			// a second look costs one tick; on a backend where the list
			// could read empty for an instant, one look would demote a live
			// watch to the rescan net for good.
			if len(iw.w.WatchList()) == 0 {
				if misses++; misses >= 2 {
					iw.lost(s)
					return
				}
				continue
			}
			misses = 0
		case ev, ok := <-iw.w.Events:
			if !ok {
				iw.lost(s)
				return
			}
			if !relevantEvent(ev) {
				continue
			}
			id := idFromIssuePath(ev.Name, issuesPath)
			if id == "" {
				continue
			}
			applyEventUntil(s, id, iw.stop)
		case err, ok := <-iw.w.Errors:
			if !ok {
				iw.lost(s)
				return
			}
			// fsnotify's error channel carries the kernel queue-full
			// signal: events were DROPPED, and nothing will resend
			// them. The watcher stays alive and the index is rebuilt
			// from disk — the second lossy carrier gets the same net
			// as a refused watch — on another goroutine: this one must
			// keep draining the unbuffered event channel, or the queue
			// that just overflowed fills again during the repair. Log
			// once per session so a host that overflows every second
			// does not flood the log. Wrap the flag in a tiny mutex
			// because the events + errors selects run on the same
			// goroutine but the linter cannot prove that.
			mu.Lock()
			first := !seenErr
			seenErr = true
			mu.Unlock()
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				s.rebuildAsync("kernel event queue overflowed")
				if first {
					s.getLogger().Warn("native index watcher: kernel event queue overflowed; index rebuilt from disk (further overflows rebuild silently this session)")
				}
				continue
			}
			if first {
				s.getLogger().Error("native index watcher: fsnotify error: %v — board index may serve stale reads until the next write event or restart (further watcher errors suppressed this session)", err)
			}
		}
	}
}

// relevantEvent screens out filesystem events that don't touch a
// committed issue file. The atomic-rename path writes to a `.tmp`
// shadow before moving it into place; we only react to the final
// `.json` filename so we don't reload from a half-written file.
// Chmod-only events are noise on Linux (umask churn during writes).
func relevantEvent(ev fsnotify.Event) bool {
	if ev.Op&fsnotify.Chmod == ev.Op {
		return false
	}
	name := filepath.Base(ev.Name)
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	if strings.HasSuffix(name, ".tmp") || strings.Contains(name, ".tmp.") {
		return false
	}
	return true
}

// idFromIssuePath inverts issuePath: strip the issues/ prefix and
// `.json` suffix, then decode the encoded segment back to the issue
// id. Returns "" when the event isn't for a file we manage (e.g. a
// sub-directory, an unrelated entry that snuck into issues/).
func idFromIssuePath(eventPath, issuesPath string) string {
	rel, err := filepath.Rel(issuesPath, eventPath)
	if err != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) {
		return ""
	}
	encoded := strings.TrimSuffix(rel, ".json")
	if encoded == rel {
		return ""
	}
	return decodeID(encoded)
}

// eventReadRetries are the pauses between attempts to re-read a card an
// event announced and whose file would not read. On Windows the writer's
// handle on the freshly renamed file (or a scanner's) can outlive the event
// by a few milliseconds and the read fails with a sharing violation; the
// first version of this watcher gave up on that error and left the card
// dropped (REMOVE of the replaced name arrives before the CREATE) or
// missing for good. The total wait is bounded well under a second so the
// loop keeps draining the event channel.
var eventReadRetries = []time.Duration{20 * time.Millisecond, 50 * time.Millisecond, 120 * time.Millisecond, 250 * time.Millisecond}

// applyEvent reconciles s.index with disk for one issue id. The
// store mutex serialises this against the parent process's own
// mutators so a watcher-driven reload can never race with a mutator
// that's mid-write — the disk read MUST happen inside the lock,
// otherwise a local mutator that runs between the read and the
// index update would have its newer state overwritten by the older
// snapshot we read pre-lock.
//
// The event's op is deliberately NOT trusted: an atomic replace of a card
// reaches us as REMOVE (the replaced name) then CREATE, and dropping the
// card on the REMOVE alone made it vanish whenever the following re-read
// failed. Whatever the op, disk is asked: a file that exists is indexed, a
// file that is gone is dropped, a file that exists but will not read is
// retried a few times and then left to the periodic verification pass
// (verifyInterval), which re-reads everything.
func applyEvent(s *Store, id string, op fsnotify.Op) {
	_ = op
	applyEventUntil(s, id, nil)
}

// applyEventUntil is applyEvent that stops waiting between retries when stop
// closes (the watcher is being closed). A nil stop never fires.
func applyEventUntil(s *Store, id string, stop <-chan struct{}) {
	var err error
	for attempt := 0; ; attempt++ {
		// Every mutation is marked dirty like an in-process write: an
		// overflow-triggered rebuild scans the disk with the mutex released
		// while this loop keeps draining the backlog, and its swap must not
		// revert what the events applied after the scan read those files.
		if err = s.syncIssueFromDisk(id); err == nil {
			return
		}
		if attempt >= len(eventReadRetries) {
			break
		}
		select {
		case <-stop:
			return
		case <-time.After(eventReadRetries[attempt]):
		}
	}
	s.getLogger().Warn("native index watcher: card %s changed on disk but could not be read (%v) — keeping the last indexed copy; the periodic verification will retry", id, err)
}
