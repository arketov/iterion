package dispatcher

import (
	"encoding/json"
	"os"
	"path/filepath"

	iterlog "github.com/arketov/iterion/pkg/log"
)

// sourceHoldEntry is one adopted source hold on disk.
type sourceHoldEntry struct {
	IssueID     string `json:"issue_id"`
	Identifier  string `json:"identifier"`
	RunID       string `json:"run_id"`
	SourceState string `json:"source_state"`
}

// sourceHoldJournal persists the ADOPTED source holds to
// <storeDir>/dispatcher/source-holds.json (the same dispatcher-local footing as
// claims.json: no Issue field, no cloud twin).
//
// Only adopted holds are durable. An adopted card sits in an eligible column
// (in_progress) with no claim, and a claim cannot carry it across a restart:
// the boot sweep releases a dead pid's claim on Unix, Windows never sweeps it,
// and the claim watchdog is opt-in. A non-adopted hold is deliberately
// memory-only: a restart costs one claim → refusal → hold cycle, which also
// lets a reverted bot source resume normally.
//
// Nil-safe: an empty storeDir yields a nil journal and every method is a no-op.
type sourceHoldJournal struct {
	path   string
	logger *iterlog.Logger
}

func newSourceHoldJournal(storeDir string, logger *iterlog.Logger) *sourceHoldJournal {
	if storeDir == "" {
		return nil
	}
	return &sourceHoldJournal{
		path:   filepath.Join(storeDir, "dispatcher", "source-holds.json"),
		logger: logger,
	}
}

// Load returns the journalled holds, always non-nil. A missing file is silent;
// a corrupt or unreadable one is logged and ignored so it cannot wedge startup.
func (j *sourceHoldJournal) Load() map[string]*sourceHold {
	out := map[string]*sourceHold{}
	if j == nil {
		return out
	}
	raw, err := os.ReadFile(j.path)
	if err != nil {
		if !os.IsNotExist(err) {
			j.logger.Warn("dispatcher: source hold journal %s: %v — starting empty", j.path, err)
		}
		return out
	}
	var list []sourceHoldEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		j.logger.Warn("dispatcher: source hold journal %s unreadable (%v) — starting empty", j.path, err)
		return out
	}
	for _, e := range list {
		if e.IssueID == "" || e.RunID == "" {
			continue
		}
		out[e.IssueID] = &sourceHold{
			identifier:  e.Identifier,
			runID:       e.RunID,
			sourceState: e.SourceState,
			adopted:     true,
		}
	}
	return out
}

// Save rewrites the journal atomically (tmp + rename) with the adopted holds
// of holds; an empty set writes an empty array rather than deleting the file
// (no Windows delete races). A failure is logged, not fatal: the in-memory
// state stays correct for this process, only restart recovery degrades.
func (j *sourceHoldJournal) Save(holds map[string]*sourceHold) {
	if j == nil {
		return
	}
	list := make([]sourceHoldEntry, 0, len(holds))
	for id, h := range holds {
		if h.adopted {
			list = append(list, sourceHoldEntry{IssueID: id, Identifier: h.identifier, RunID: h.runID, SourceState: h.sourceState})
		}
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		j.logger.Warn("dispatcher: source hold journal marshal: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		j.logger.Warn("dispatcher: source hold journal dir: %v", err)
		return
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		j.logger.Warn("dispatcher: source hold journal write: %v", err)
		return
	}
	if err := os.Rename(tmp, j.path); err != nil {
		j.logger.Warn("dispatcher: source hold journal rename: %v", err)
	}
}
