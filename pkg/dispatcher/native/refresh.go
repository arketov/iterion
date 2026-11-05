package native

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// IndexRefresher is the optional capability the board's HTTP list handler
// asks a store for: bring the in-memory index in line with the directory the
// cards live in. The filesystem *Store implements it; the cloud Mongo store
// has no index to refresh and does not.
type IndexRefresher interface {
	// RefreshIndex reconciles the index with disk. With force it re-reads
	// every card (an operator pressed "refresh"). Without, it only compares
	// the set of card files with the set of indexed cards — a ReadDir and no
	// reads — and re-reads everything when the two disagree.
	RefreshIndex(force bool) error
}

// defaultVerifyInterval is how often a store whose watch IS armed still
// reconciles with disk. A watch is a lossy carrier on every host, and on
// Windows the loss is routine: an atomic replace (temp file, rename over the
// card) is reported as REMOVE then CREATE, and a re-read that races the
// writer's handle fails and was never retried — the card stayed dropped or
// stale until the daemon restarted. The cost is the one Reconcile's doc
// measures (a few ms for a few hundred cards, off the store mutex).
const defaultVerifyInterval = 10 * time.Second

// verifyIntervalOverride lets a test pin the cadence; see fireSeam for why it
// is an atomic.
var verifyIntervalOverride atomic.Pointer[time.Duration]

// verifyInterval resolves ITERION_NATIVE_INDEX_VERIFY when the watcher loop
// starts: the same spellings as ITERION_NATIVE_INDEX_RESCAN ("off", zero or a
// negative value disables; a duration or a number of seconds is itself). An
// unreadable value falls back to the default — never to "off".
func verifyInterval() time.Duration {
	if d := verifyIntervalOverride.Load(); d != nil {
		return *d
	}
	d, _ := intervalFromEnv("ITERION_NATIVE_INDEX_VERIFY", defaultVerifyInterval)
	return d
}

// RefreshIndex implements IndexRefresher.
func (s *Store) RefreshIndex(force bool) error {
	if !force {
		drifted, err := s.indexDrifted()
		if err != nil {
			return err
		}
		if !drifted {
			return nil
		}
	}
	return s.Reconcile()
}

// indexDrifted reports whether the card files under issues/ and the indexed
// cards are different sets. It lists the directory and reads no card.
func (s *Store) indexDrifted() (bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, issuesDir))
	if err != nil {
		return false, err
	}
	onDisk := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		onDisk[decodeID(strings.TrimSuffix(e.Name(), ".json"))] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range onDisk {
		if _, ok := s.index[id]; ok {
			continue
		}
		if _, bad := s.unreadableIDs[id]; bad {
			continue // present, known unreadable: re-scanning would not change it
		}
		return true, nil
	}
	for id := range s.index {
		if _, ok := onDisk[id]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// idSet turns a scan's unreadable map into a set of ids.
func idSet(m map[string]error) map[string]struct{} {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(m))
	for id := range m {
		out[id] = struct{}{}
	}
	return out
}

// adoptFromDiskLocked is the miss path of a by-id lookup: the index does not
// know the id, but the card may have been written by another process and its
// event lost. It reads the file and, when it is a real card, indexes it.
// Anything else — an id that is not a card id, a missing or unreadable file —
// is a miss, exactly as before. Caller holds mu.
func (s *Store) adoptFromDiskLocked(id string) (*Issue, error) {
	if s.closed || validateIssueID(id) != nil {
		return nil, tracker.ErrNotFound
	}
	iss, err := s.readIssueFromDisk(id)
	if err != nil || iss.ID != id {
		return nil, tracker.ErrNotFound
	}
	s.setIndexLocked(id, iss)
	return cloneIssue(iss), nil
}

// syncIssueFromDisk makes the index entry for id equal what is on disk now:
// the card when its file reads, no entry when the file is gone. A file that is
// present but cannot be read (a sharing violation while another process still
// holds the handle it renamed over the card, a half-written file) returns the
// error and changes nothing — the caller retries; a stale-but-readable card
// beats a forced 404.
//
// The read is inside the store mutex on purpose: see applyEvent.
func (s *Store) syncIssueFromDisk(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	iss, err := s.readIssueFromDisk(id)
	switch {
	case err == nil:
		s.setIndexLocked(id, iss)
		return nil
	case errors.Is(err, tracker.ErrNotFound):
		s.dropIndexLocked(id)
		return nil
	default:
		return err
	}
}
