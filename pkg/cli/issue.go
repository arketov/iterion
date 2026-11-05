package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/forge"
	forgegithub "github.com/arketov/iterion/pkg/forge/github"
	"github.com/arketov/iterion/pkg/server"
	"github.com/arketov/iterion/pkg/store"
)

// IssueCommonOptions are flags shared by every `iterion issue` subcommand.
type IssueCommonOptions struct {
	StoreDir string
}

// openNativeStore resolves <store-dir>/dispatcher and opens the native
// store there. The directory and board.json are created on first call.
func openNativeStore(opts IssueCommonOptions) (*native.Store, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	storeDir := store.ResolveStoreDir(cwd, opts.StoreDir)
	root := filepath.Join(storeDir, "dispatcher")
	s, err := native.NewStore(root)
	if err != nil {
		return nil, "", err
	}
	return s, root, nil
}

// IssueCreateOptions captures the flags for `iterion issue create`.
type IssueCreateOptions struct {
	IssueCommonOptions
	Title    string
	Body     string
	State    string
	Labels   []string
	Priority int
	Assignee string
	Blockers []string
	Fields   []string // custom-field key=value pairs (Issue.Fields)
	Bot      string   // typed workflow override consumed by the dispatcher
	BotArgs  []string // typed dispatcher var overrides as key=value (Issue.BotArgs)
}

// RunIssueCreate creates a new issue in the native tracker.
func RunIssueCreate(p *Printer, opts IssueCreateOptions) error {
	if opts.Title == "" {
		return errors.New("issue create: --title is required")
	}
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	fields, err := parseFieldPairs(opts.Fields)
	if err != nil {
		return err
	}
	botArgs, err := parseBotArgs(opts.BotArgs)
	if err != nil {
		return err
	}
	iss := native.Issue{
		Title:    opts.Title,
		Body:     opts.Body,
		State:    opts.State,
		Labels:   opts.Labels,
		Priority: opts.Priority,
		Assignee: opts.Assignee,
		Blockers: opts.Blockers,
		Fields:   fields,
		Bot:      opts.Bot,
		BotArgs:  botArgs,
	}
	got, err := s.Create(iss)
	if err != nil {
		return err
	}
	if p.Format == OutputJSON {
		jsonIssue(p, got)
		return nil
	}
	p.Line("Created %s", shortID(got.ID))
	p.KV("State", got.State)
	if got.Title != "" {
		p.KV("Title", got.Title)
	}
	return nil
}

// IssueListOptions captures the flags for `iterion issue list`.
type IssueListOptions struct {
	IssueCommonOptions
	States    []string
	Labels    []string
	Assignee  string
	OnlyClaim bool
	OnlyFree  bool
}

// RunIssueList prints issues from the native tracker.
func RunIssueList(p *Printer, opts IssueListOptions) error {
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	f := native.ListFilter{
		States:   opts.States,
		Labels:   opts.Labels,
		Assignee: opts.Assignee,
	}
	switch {
	case opts.OnlyClaim:
		t := true
		f.Claimed = &t
	case opts.OnlyFree:
		t := false
		f.Claimed = &t
	}
	issues, err := s.List(f)
	if err != nil {
		return err
	}
	if p.Format == OutputJSON {
		jsonIssues(p, issues)
		return nil
	}
	rows := make([][]string, 0, len(issues))
	for _, iss := range issues {
		rows = append(rows, []string{
			shortID(iss.ID),
			iss.State,
			strconv.Itoa(iss.Priority),
			truncate(iss.Title, 50),
			strings.Join(iss.Labels, ","),
			iss.Assignee,
		})
	}
	p.Table([]string{"ID", "STATE", "PRIO", "TITLE", "LABELS", "ASSIGNEE"}, rows)
	return nil
}

// IssueRefOptions identifies a single issue by ID or prefix.
type IssueRefOptions struct {
	IssueCommonOptions
	IDOrPrefix string
}

// RunIssueShow prints a single issue.
func RunIssueShow(p *Printer, opts IssueRefOptions) error {
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	id, err := s.Resolve(opts.IDOrPrefix)
	if err != nil {
		return err
	}
	iss, err := s.Get(id)
	if err != nil {
		return err
	}
	if p.Format == OutputJSON {
		jsonIssue(p, iss)
		return nil
	}
	p.Header(iss.Title)
	p.KV("ID", iss.ID)
	p.KV("State", iss.State)
	p.KV("Priority", strconv.Itoa(iss.Priority))
	if iss.Assignee != "" {
		p.KV("Assignee", iss.Assignee)
	}
	if len(iss.Labels) > 0 {
		p.KV("Labels", strings.Join(iss.Labels, ", "))
	}
	if len(iss.Blockers) > 0 {
		p.KV("Blockers", strings.Join(iss.Blockers, ", "))
	}
	if iss.Claim != "" {
		p.KV("Claim", iss.Claim)
	}
	// The card's run: the pointer AND what the run turned out to be. Printing
	// the pointer alone is the defect #1501 names — a card whose run died
	// reads exactly like a card that was dispatched and worked.
	if iss.LastRunID != "" {
		p.KV("Last run", iss.LastRunID)
	}
	if outcome := FormatRunOutcome(iss.LastRunStatus, iss.LastRunErrorCode, iss.LastRunAt); outcome != "" {
		p.KV("Last run result", outcome)
	}
	if iss.LastRunError != "" {
		p.KV("Last run error", iss.LastRunError)
	}
	if len(iss.Fields) > 0 {
		data, _ := json.MarshalIndent(iss.Fields, "  ", "  ")
		p.Line("  Fields:")
		p.Line("    %s", string(data))
	}
	if iss.Body != "" {
		p.Blank()
		p.Line("%s", iss.Body)
	}
	return nil
}

// IssueMoveOptions moves an issue to a new state.
type IssueMoveOptions struct {
	IssueCommonOptions
	IDOrPrefix string
	To         string
}

// RunIssueMove transitions an issue.
func RunIssueMove(p *Printer, opts IssueMoveOptions) error {
	if opts.To == "" {
		return errors.New("issue move: --to is required")
	}
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	id, err := s.Resolve(opts.IDOrPrefix)
	if err != nil {
		return err
	}
	// The CLI is an operator surface: moving a card out of a terminal
	// state is the sanctioned reopen.
	iss, err := native.SetStateOrReopen(s, id, opts.To)
	if err != nil {
		return err
	}
	if p.Format == OutputJSON {
		jsonIssue(p, iss)
		return nil
	}
	p.Line("Moved %s → %s", shortID(iss.ID), iss.State)
	return nil
}

// IssueUpdateOptions captures partial-update fields. Nil pointer means "unchanged".
type IssueUpdateOptions struct {
	IssueCommonOptions
	IDOrPrefix string
	Title      *string
	Body       *string
	Labels     *[]string
	Priority   *int
	Assignee   *string
	Blockers   *[]string
	Fields     []string // key=value (set or replace)
	ClearField []string
	// Bot sets the per-ticket bot override; an empty string clears it.
	Bot *string
	// BotArgs are --bot-arg key=value pairs MERGED over the card's existing
	// bot args (unlike the store's wholesale patch, a terminal user typing one
	// --bot-arg does not expect the others to vanish). ClearBotArg removes
	// single keys, applied before BotArgs. The server-owned classes (publish
	// grant, reserved launch context) are never writable from here — same
	// filter as `issue create` and the HTTP patch.
	BotArgs     []string
	ClearBotArg []string
	// ClearLastRun drops the issue's last_run pointer (Store.SetLastRun with
	// empty strings — the clear its own contract documents). It is the
	// operator's way back to a FRESH launch: while the pointer names a
	// resumable run, the dispatcher resumes that run instead of minting a new
	// one (resolveRunID → resumableRunID), so a ticket whose run died in a way
	// resuming cannot fix had no exit but hand-editing the issue JSON.
	// The run history (Issue.Runs) is kept — the runs still happened.
	ClearLastRun bool
}

// RunIssueUpdate applies the patch.
func RunIssueUpdate(p *Printer, opts IssueUpdateOptions) error {
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	id, err := s.Resolve(opts.IDOrPrefix)
	if err != nil {
		return err
	}
	fields, err := parseFieldPairs(opts.Fields)
	if err != nil {
		return err
	}
	for _, k := range opts.ClearField {
		if fields == nil {
			fields = map[string]any{}
		}
		fields[k] = nil
	}
	botArgs, err := parseBotArgs(opts.BotArgs)
	if err != nil {
		return err
	}
	var botArgsPatch *map[string]string
	if len(botArgs) > 0 || len(opts.ClearBotArg) > 0 {
		current, err := s.Get(id)
		if err != nil {
			return err
		}
		merged := make(map[string]string, len(current.BotArgs)+len(botArgs))
		for k, v := range current.BotArgs {
			merged[k] = v
		}
		for _, k := range opts.ClearBotArg {
			k = strings.TrimSpace(k)
			if native.IsReservedLaunchArg(k) {
				return fmt.Errorf("issue update: --clear-bot-arg %q is a dispatcher-owned launch variable and cannot be cleared", k)
			}
			delete(merged, k)
		}
		for k, v := range native.DropServerMintedLaunchVars(botArgs) {
			merged[k] = v
		}
		botArgsPatch = &merged
	}
	// Before the patch: refusing after it would apply --title and then error,
	// leaving the operator with a half-done command they did not ask for.
	if opts.ClearLastRun {
		current, err := s.Get(id)
		if err != nil {
			return err
		}
		if err := refuseClearWhileRunAlive(opts.IssueCommonOptions, current.LastRunID); err != nil {
			return err
		}
	}
	patch := native.Patch{
		Title:    opts.Title,
		Body:     opts.Body,
		Labels:   opts.Labels,
		Priority: opts.Priority,
		Assignee: opts.Assignee,
		Blockers: opts.Blockers,
		Fields:   fields,
		Bot:      opts.Bot,
		BotArgs:  botArgsPatch,
	}
	iss, err := s.Update(id, patch)
	if err != nil {
		return err
	}
	if opts.ClearLastRun {
		if err := s.SetLastRun(id, "", ""); err != nil {
			return err
		}
		if iss, err = s.Get(id); err != nil {
			return err
		}
	}
	if p.Format == OutputJSON {
		jsonIssue(p, iss)
		return nil
	}
	p.Line("Updated %s", shortID(iss.ID))
	if opts.ClearLastRun {
		p.Line("Cleared the last-run pointer — the next dispatch starts a fresh run instead of resuming.")
	}
	return nil
}

// refuseClearWhileRunAlive stops `--clear-last-run` from forgetting a run that
// is still ALIVE. The pointer is the sole persisted input to the dispatcher's
// sibling guard (lastRunForbidsFresh, pkg/dispatcher/retry.go): with it gone,
// the next dispatch mints a fresh run from the workflow entry — and if the
// original is still running or parked on a question, that is two agents on one
// ticket, two branches and two PRs for one issue.
//
// The flag's whole purpose is to defeat that guard for a run that is dead but
// RESUMABLE (`failed_resumable` / `cancelled` — the documented case, and both
// forbidden statuses), so the discrimination that matters is dead-vs-live, not
// terminal-vs-not. A run that cannot be read is not protected: it is already
// gone as far as the dispatcher is concerned.
func refuseClearWhileRunAlive(opts IssueCommonOptions, runID string) error {
	run := liveRunOf(opts, runID)
	if run == nil {
		return nil
	}
	return fmt.Errorf(
		"issue update: run %s is still %s — clearing the pointer now would let the next dispatch start a SECOND run on this ticket; stop or answer it first (`iterion inspect --run-id %s`)",
		shortID(runID), run.Status, runID)
}

// liveRunOf returns the run when it exists and is still alive (running, queued
// or paused), nil otherwise — including when the store cannot be read: a run
// that cannot be established is not protected.
func liveRunOf(opts IssueCommonOptions, runID string) *store.Run {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	rs, err := store.New(store.ResolveStoreDir(cwd, opts.StoreDir))
	if err != nil {
		// No readable store — nothing to protect, and refusing here would
		// block the escape hatch over a condition we cannot establish.
		return nil
	}
	run, err := rs.LoadRun(context.Background(), runID)
	if err != nil || run == nil {
		return nil
	}
	switch run.Status {
	case store.RunStatusRunning, store.RunStatusQueued,
		store.RunStatusPausedWaitingHuman, store.RunStatusPausedOperator:
		return run
	}
	return nil
}

// RunIssueClose moves the issue to the first terminal state on the board.
func RunIssueClose(p *Printer, opts IssueRefOptions) error {
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	id, err := s.Resolve(opts.IDOrPrefix)
	if err != nil {
		return err
	}
	board, err := s.Board()
	if err != nil {
		return err
	}
	terminal := ""
	for _, st := range board.States {
		if st.Terminal {
			terminal = st.Name
			break
		}
	}
	if terminal == "" {
		return errors.New("issue close: board has no terminal state — declare one or use `issue move`")
	}
	filed, err := s.SetState(id, terminal)
	if err != nil {
		return err
	}
	// Closing is the operator ACKNOWLEDGING the ticket, so drop any
	// dispatcher give-up stamp on it. Moving a ticket expires the stamp on
	// its own, but closing one the dispatcher already filed into this very
	// state changes nothing — and would leave the card sitting in the
	// pipeline board's needs-attention lane after the operator closed it.
	// Best-effort, like the pipeline board's Close: SetState has already
	// committed, so failing here would report a close that DID happen as a
	// failure. A surviving stamp costs a card in the wrong lane, not
	// correctness — say so and carry on.
	if err := s.SetGaveUp(id, nil); err != nil {
		// stderr, not the Printer: `--json` writes a single document to
		// stdout, and a warning spliced in front of it turns a scripted
		// close into a parse error on a path that otherwise succeeded.
		fmt.Fprintf(os.Stderr, "warning: could not clear the dispatcher give-up stamp on %s: %v\n", shortID(id), err)
	}
	// Same for the re-read: the close has landed, so fall back to SetState's
	// snapshot rather than report a committed close as a failure.
	iss := filed
	if refreshed, getErr := s.Get(id); getErr == nil {
		iss = refreshed
	}
	if p.Format == OutputJSON {
		jsonIssue(p, iss)
		return nil
	}
	p.Line("Closed %s → %s", shortID(iss.ID), iss.State)
	return nil
}

// IssueDeleteOptions captures the flags for `iterion issue delete`.
type IssueDeleteOptions struct {
	IssueCommonOptions
	IDOrPrefix string
	// Yes skips the confirmation prompt (required when stdin is not a terminal).
	Yes bool
	// Stdin overrides where the confirmation is read from (tests); nil means
	// os.Stdin, and then a non-terminal stdin without Yes is refused.
	Stdin io.Reader
}

// RunIssueDelete permanently removes a card from the native board. `close`
// keeps the card (terminal state); delete does not — so it asks first.
func RunIssueDelete(p *Printer, opts IssueDeleteOptions) error {
	s, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	// An empty reference would prefix-match every card, and on a board with
	// one card delete it: refuse before Resolve, for an irreversible command.
	if strings.TrimSpace(opts.IDOrPrefix) == "" {
		return errors.New("issue delete: an issue id or prefix is required")
	}
	id, err := s.Resolve(opts.IDOrPrefix)
	if err != nil {
		return err
	}
	iss, err := s.Get(id)
	if err != nil {
		return err
	}
	// The studio's pipeline-board delete answers 409 while the card's run is
	// alive; deleting under it would leave the dispatcher writing to a card
	// that no longer exists. Cancel or finish the run first.
	if run := liveRunOf(opts.IssueCommonOptions, iss.LastRunID); run != nil {
		return fmt.Errorf("issue delete: run %s is still %s — cancel or finish it first (`iterion inspect --run-id %s`)",
			shortID(iss.LastRunID), run.Status, iss.LastRunID)
	}
	if !opts.Yes {
		in := opts.Stdin
		if in == nil {
			if !IsTTY() {
				return errors.New("issue delete: refusing to delete without confirmation on a non-interactive stdin — pass --yes")
			}
			in = os.Stdin
		}
		// stderr so `--json` stdout stays a single document.
		fmt.Fprintf(os.Stderr, "Permanently delete %s %q? This cannot be undone (use `issue close` to keep it). [y/N]: ", shortID(id), iss.Title)
		line, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
		default:
			return errors.New("issue delete: aborted, nothing was deleted")
		}
	}
	if err := s.Delete(id); err != nil {
		return err
	}
	if p.Format == OutputJSON {
		p.JSON(map[string]any{"deleted": id})
		return nil
	}
	p.Line("Deleted %s", shortID(id))
	return nil
}

// IssueImportOptions captures the flags for `iterion issue import`: mirror a
// self-hosted forge repo's issues into the native board.
type IssueImportOptions struct {
	IssueCommonOptions
	Forge    string // github | forgejo | gitlab
	Repo     string // owner/name
	TokenEnv string // NAME of the env var holding the forge token (never the value)
	BaseURL  string // forge API base; empty = provider default
	Since    string // RFC3339; empty = full re-sync
	// MinAuthorRole is the trust threshold (gitlab vocabulary; "" →
	// developer) above which an issue author's fresh card is stamped
	// triage:auto instead of parked needs:approval.
	MinAuthorRole string
	// Project is a forge PROJECT board as "<owner>/<number>". When set, the
	// issue sync is followed by a project pass that mirrors the board's Status
	// onto the cards' columns and its Area/Mode/Priority onto their labels
	// (ADR-097). It hydrates cards, never creates them: a project item carries
	// no author, so creating from one would bypass the ingest trust gate.
	Project string
	// ProjectOwnerKind is "org" (default) or "user" — the namespace the
	// project lives under. Explicit rather than probed: the provider's lookup
	// differs, and guessing would resolve the wrong owner on a typo.
	ProjectOwnerKind string
}

// importedSoFar prices a refused import in what it already WROTE. The issue
// pass streams each page onto the board as it arrives, so the cards of the
// pages that came stay there — and the three counters below are printed only
// on the success path. Without this, an operator cannot tell a refusal that
// wrote 20 000 cards from one that wrote none, and this command is the entry
// point that keeps no high-water mark and has no next pass to reconcile
// behind it: re-running is their only move, and its cost is what landed.
//
// ISSUES, not entries: the number is the sync core's `mirrored`, which counts
// what reached the board — the pull requests the issues endpoint serves
// alongside them are discarded before the upsert, and the core's own refusals
// price the READ in entries. Two counts of one window under one word is two
// numbers an operator cannot reconcile.
func importedSoFar(synced, created, updated int) string {
	return fmt.Sprintf(" (mirrored %d issues before the refusal: %d cards created, %d updated)", synced, created, updated)
}

// RunIssueImport imports a forge repo's issues into the native board, reusing
// the store-agnostic sync core via server.ImportForgeIssues. The token is read
// only from the NAMED env var (secrets discipline — never a flag value). The
// import is idempotent: re-running upserts existing cards instead of
// duplicating them.
func RunIssueImport(p *Printer, opts IssueImportOptions) error {
	provider := forge.Provider(strings.TrimSpace(opts.Forge))
	if !provider.Valid() {
		return fmt.Errorf("issue import: --forge must be one of github|forgejo|gitlab, got %q", opts.Forge)
	}
	if strings.TrimSpace(opts.Repo) == "" {
		return errors.New("issue import: --repo is required (owner/name)")
	}
	if strings.TrimSpace(opts.TokenEnv) == "" {
		return errors.New("issue import: --token-env is required (name of the env var holding the forge token)")
	}
	token := os.Getenv(opts.TokenEnv)
	if token == "" {
		return fmt.Errorf("issue import: env var %q is empty or unset", opts.TokenEnv)
	}
	var since time.Time
	if s := strings.TrimSpace(opts.Since); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return fmt.Errorf("issue import: --since must be RFC3339, got %q: %w", opts.Since, err)
		}
		since = t
	}
	// Resolve --project BEFORE any network call: a typo in the ref, or a
	// provider with no project board, must cost nothing.
	projectRef, wantProject, err := resolveImportProjectRef(provider, opts)
	if err != nil {
		return err
	}
	board, _, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	synced, created, updated, err := server.ImportForgeIssues(
		context.Background(), provider, strings.TrimSpace(opts.BaseURL), token, opts.Repo, board, since,
		strings.TrimSpace(opts.MinAuthorRole))
	if err != nil {
		return fmt.Errorf("issue import: %w%s", err, importedSoFar(synced, created, updated))
	}
	var projectRes server.ProjectImportResult
	if wantProject {
		bc, err := boardClientFor(provider, strings.TrimSpace(opts.BaseURL), token)
		if err != nil {
			return err
		}
		projectRes, err = server.ImportProjectBoard(context.Background(), bc, projectRef, provider, board, nil)
		if err != nil {
			return fmt.Errorf("issue import: %w%s", err, importedSoFar(synced, created, updated))
		}
	}
	if p.Format == OutputJSON {
		p.JSON(struct {
			Synced  int                         `json:"synced"`
			Created int                         `json:"created"`
			Updated int                         `json:"updated"`
			Project *server.ProjectImportResult `json:"project,omitempty"`
		}{Synced: synced, Created: created, Updated: updated, Project: projectResultPtr(wantProject, projectRes)})
		return nil
	}
	p.Line("Imported %s from %s", opts.Repo, provider)
	// Synced is the window mirrored, Created/Updated the cards it had to
	// write: a re-import of an unchanged repo writes nothing, and reporting
	// only the writes would read as an import that found nothing.
	p.KV("Synced", strconv.Itoa(synced))
	p.KV("Created", strconv.Itoa(created))
	p.KV("Updated", strconv.Itoa(updated))
	if wantProject {
		p.Blank()
		p.Line("Project board %s", projectRef)
		p.KV("Items", strconv.Itoa(projectRes.Items))
		p.KV("Moved", strconv.Itoa(projectRes.Moved))
		p.KV("Labelled", strconv.Itoa(projectRes.Labelled))
		if projectRes.Conflicts > 0 {
			p.KV("Conflicts", strconv.Itoa(projectRes.Conflicts))
		}
		if projectRes.ReopenedTerminal > 0 {
			p.KV("Reopened (board move)", strconv.Itoa(projectRes.ReopenedTerminal))
		}
		if projectRes.RefusedTerminal > 0 {
			p.KV("Refused (terminal)", strconv.Itoa(projectRes.RefusedTerminal))
			// Name the cards: reopening a finished one is a native gesture, and
			// the ids ARE the command that performs it.
			for _, id := range projectRes.RefusedCards {
				p.Line("    %s — iterion issue move %s <state>", id, id)
			}
		}
		if projectRes.SkippedArchived > 0 {
			p.KV("Skipped (archived)", strconv.Itoa(projectRes.SkippedArchived))
		}
		if projectRes.SkippedNoCard > 0 {
			p.KV("Skipped (no card yet)", strconv.Itoa(projectRes.SkippedNoCard))
			// Name the repos: the operator's next command is one issue import
			// per line, and a bare count would not say which.
			p.Line("  Run the issue import for these repos first:")
			for _, m := range projectRes.MissingRepos {
				p.Line("    %s (%d item(s))", m.Repo, m.Count)
			}
		}
	}
	return nil
}

func projectResultPtr(want bool, res server.ProjectImportResult) *server.ProjectImportResult {
	if !want {
		return nil
	}
	return &res
}

// resolveImportProjectRef validates --project/--project-owner-kind. It returns
// ok=false when no project was asked for.
func resolveImportProjectRef(provider forge.Provider, opts IssueImportOptions) (forge.ProjectRef, bool, error) {
	raw := strings.TrimSpace(opts.Project)
	if raw == "" {
		return forge.ProjectRef{}, false, nil
	}
	ref, err := forge.ParseProjectRef(raw)
	if err != nil {
		return forge.ProjectRef{}, false, fmt.Errorf("issue import: --project must be <owner>/<number> (e.g. SocialGouv/203): %w", err)
	}
	switch kind := strings.TrimSpace(opts.ProjectOwnerKind); kind {
	case "":
		ref.OwnerKind = forge.ProjectOwnerOrg
	case string(forge.ProjectOwnerOrg), string(forge.ProjectOwnerUser):
		ref.OwnerKind = forge.ProjectOwnerKind(kind)
	default:
		return forge.ProjectRef{}, false, fmt.Errorf("issue import: --project-owner-kind must be org or user, got %q", kind)
	}
	if provider != forge.ProviderGitHub {
		return forge.ProjectRef{}, false, fmt.Errorf("issue import: provider %q exposes no project board (only github has one today)", provider)
	}
	return ref, true, nil
}

// boardClientFor builds the provider's project-board client from a raw token,
// refusing providers that do not implement the capability.
func boardClientFor(provider forge.Provider, baseURL, token string) (forge.BoardClient, error) {
	if baseURL == "" {
		baseURL = forge.DefaultBaseURL(provider)
	}
	var admin forge.Admin
	switch provider {
	case forge.ProviderGitHub:
		admin = forgegithub.New(http.DefaultClient, baseURL, token)
	default:
		return nil, fmt.Errorf("issue import: provider %q exposes no project board", provider)
	}
	bc, ok := forge.AsBoardClient(admin)
	if !ok {
		return nil, fmt.Errorf("issue import: provider %q exposes no project board", provider)
	}
	return bc, nil
}

// RunIssueBoardShow prints the current board.json.
func RunIssueBoardShow(p *Printer, opts IssueCommonOptions) error {
	s, root, err := openNativeStore(opts)
	if err != nil {
		return err
	}
	b, err := s.Board()
	if err != nil {
		return err
	}
	if p.Format == OutputJSON {
		p.JSON(b)
		return nil
	}
	p.Header("Board")
	p.KV("Location", filepath.Join(root, "board.json"))
	p.Blank()
	p.Line("States:")
	for _, st := range b.States {
		tag := ""
		if st.Eligible {
			tag += " (eligible)"
		}
		if st.Terminal {
			tag += " (terminal)"
		}
		p.Line("  - %s%s", st.Name, tag)
	}
	if len(b.Fields) > 0 {
		p.Blank()
		p.Line("Fields:")
		for _, f := range b.Fields {
			req := ""
			if f.Required {
				req = " (required)"
			}
			p.Line("  - %s : %s%s", f.Name, f.Type, req)
			if f.Type == native.FieldEnum {
				p.Line("      values: %s", strings.Join(f.EnumValues, ", "))
			}
		}
	}
	return nil
}

// IssueBoardInitOptions configures `issue board init`.
type IssueBoardInitOptions struct {
	IssueCommonOptions
	From  string // optional path to a board.json
	Force bool
}

// RunIssueBoardInit replaces the board configuration.
func RunIssueBoardInit(p *Printer, opts IssueBoardInitOptions) error {
	s, root, err := openNativeStore(opts.IssueCommonOptions)
	if err != nil {
		return err
	}
	var b *native.Board
	if opts.From != "" {
		data, err := os.ReadFile(opts.From)
		if err != nil {
			return fmt.Errorf("read %s: %w", opts.From, err)
		}
		b = &native.Board{}
		if err := json.Unmarshal(data, b); err != nil {
			return fmt.Errorf("parse %s: %w", opts.From, err)
		}
	} else {
		b = native.DefaultBoard()
	}
	if err := s.SetBoard(b); err != nil {
		return err
	}
	if p.Format == OutputJSON {
		p.JSON(b)
		return nil
	}
	p.Line("Board initialized at %s", filepath.Join(root, "board.json"))
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// parseFieldPairs converts ["k=v", "k2=42"] into typed values. Numbers,
// bools, and bare strings are auto-detected. Quoted values are kept
// literal.
func parseFieldPairs(pairs []string) (map[string]any, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	return parseKVPairs[any](pairs, kvOpts[any]{
		errFmt:        "--field expects key=value, got %q",
		trimKey:       true,
		trimVal:       true,
		requireRawKey: true,
		conv:          func(v string) any { return inferTyped(v) },
	})
}

// jsonIssue prints a card as the `--json` document, with the publish grant it
// still carries masked (native.IssueForRead): `iterion issue` is a read
// surface like the REST API and the board tools, and a card written before the
// store-boundary drop can hold a live bearer.
func jsonIssue(p *Printer, iss *native.Issue) {
	p.JSON(native.IssueForRead(iss))
}

// jsonIssues is jsonIssue for `iterion issue list --json`.
func jsonIssues(p *Printer, issues []*native.Issue) {
	p.JSON(native.IssuesForRead(issues))
}

// parseBotArgs parses repeatable --bot-arg key=value flags into the
// typed BotArgs map the dispatcher merges over rendered config vars
// (pkg/dispatcher/loop.go). Unlike parseFieldPairs (custom fields,
// map[string]any with type inference), dispatcher vars are always
// strings — matching the studio Launch form wire format — so values
// are kept verbatim (no inferTyped, no comma-splitting: glob lists like
// doc_globs=README.md,docs/**/*.md must survive intact, which is why
// the flag is registered as StringArray, not StringSlice).
func parseBotArgs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	return parseKVPairs[string](pairs, kvOpts[string]{
		errFmt:        "--bot-arg expects key=value, got %q",
		trimKey:       true,
		trimVal:       true,
		requireRawKey: true,
	})
}

func inferTyped(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func shortID(id string) string {
	s := strings.TrimPrefix(id, "native:")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}
