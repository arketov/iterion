package supervise

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/arketov/iterion/pkg/store"
)

// inboxSessionKey is the synthetic run id used when a raw Claude Code
// session's id isn't known (project-scoped inbox). When the id IS known,
// it is used directly so two concurrent sessions in one repo can be
// steered independently.
const inboxSessionKey = "_session"

// InboxInjector enqueues steering messages for a raw Claude Code session
// into an iterion-owned, file-backed inbox keyed by project. The
// `iterion __claude-hook-drain` hook (installed in the target repo's
// .claude/settings.local.json) drains it at the session's next tool/stop
// boundary, mirroring the in-process inbox-drain the claude_code delegate
// uses. Reuses the same JSONL queue format as operator chat
// (store.QueuedUserMessage), so the FIFO + delivered/consumed lifecycle +
// torn-line tolerance come for free.
type InboxInjector struct {
	store     store.RunStore
	sessionID string
}

// NewInboxInjector opens (creating if needed) the inbox for projectKey.
// sessionID, when non-empty, scopes the inbox to that session; otherwise
// a project-wide inbox is used.
func NewInboxInjector(projectKey, sessionID string) (*InboxInjector, error) {
	st, err := store.New(claudeSessionInboxRoot(projectKey))
	if err != nil {
		return nil, fmt.Errorf("supervise: open claude-session inbox: %w", err)
	}
	return &InboxInjector{store: st, sessionID: inboxRunID(sessionID)}, nil
}

// Inject implements the Injector seam. runID/nodeID from the coordinator
// are ignored — a raw session has no iterion run id or nodes; the inbox
// is keyed by the session resolved at construction.
func (i *InboxInjector) Inject(ctx context.Context, _ /*runID*/, _ /*nodeID*/, text string) error {
	msg := store.QueuedUserMessage{ID: newInboxMessageID(), Text: text}
	return i.store.AppendQueuedMessage(ctx, i.sessionID, msg)
}

// DrainClaudeInbox transitions every queued message for (projectKey,
// sessionID) to delivered and returns their texts in FIFO order. Used by
// the `__claude-hook-drain` command. publish is nil — there is no broker
// for a raw session.
func DrainClaudeInbox(ctx context.Context, projectKey, sessionID string) ([]string, error) {
	st, err := store.New(claudeSessionInboxRoot(projectKey))
	if err != nil {
		return nil, fmt.Errorf("supervise: open claude-session inbox: %w", err)
	}
	texts, _, err := store.DrainPending(ctx, st, nil, inboxRunID(sessionID))
	return texts, err
}

// inboxRunID maps a (possibly empty) session id to the synthetic store
// run id keying its inbox.
func inboxRunID(sessionID string) string {
	if sessionID == "" {
		return inboxSessionKey
	}
	return sessionID
}

// newInboxMessageID mirrors runview's id shape: a time prefix for FIFO
// ordering at the filesystem level plus a random suffix.
func newInboxMessageID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("msg_%d_%s", time.Now().UnixNano(), hex.EncodeToString(buf[:]))
}
