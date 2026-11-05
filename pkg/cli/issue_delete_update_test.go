package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/native"
	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

func seedIssueCard(t *testing.T, iss native.Issue) (storeDir, id string) {
	t.Helper()
	storeDir = t.TempDir()
	s, err := native.NewStore(filepath.Join(storeDir, "dispatcher"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := s.Create(iss)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = s.Close()
	return storeDir, got.ID
}

func reloadIssue(t *testing.T, storeDir, id string) (*native.Issue, error) {
	t.Helper()
	s, err := native.NewStore(filepath.Join(storeDir, "dispatcher"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.Get(id)
}

func TestIssueDeleteConfirmedRemovesTheCard(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "mistake"})
	var out bytes.Buffer
	err := RunIssueDelete(&Printer{W: &out, Format: OutputJSON}, IssueDeleteOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         shortID(id),
		Stdin:              strings.NewReader("y\n"),
	})
	if err != nil {
		t.Fatalf("RunIssueDelete: %v", err)
	}
	if _, err := reloadIssue(t, storeDir, id); !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("card still there after a confirmed delete: err=%v", err)
	}
}

func TestIssueDeleteDeclinedKeepsTheCard(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", ""} {
		storeDir, id := seedIssueCard(t, native.Issue{Title: "keep me"})
		err := RunIssueDelete(&Printer{W: &bytes.Buffer{}}, IssueDeleteOptions{
			IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
			IDOrPrefix:         id,
			Stdin:              strings.NewReader(answer),
		})
		if err == nil {
			t.Errorf("answer %q: want an abort error", answer)
		}
		if _, gerr := reloadIssue(t, storeDir, id); gerr != nil {
			t.Errorf("answer %q: declined delete removed the card: %v", answer, gerr)
		}
	}
}

func TestIssueDeleteYesSkipsThePrompt(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "gone"})
	if err := RunIssueDelete(&Printer{W: &bytes.Buffer{}}, IssueDeleteOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         id,
		Yes:                true,
	}); err != nil {
		t.Fatalf("RunIssueDelete --yes: %v", err)
	}
	if _, err := reloadIssue(t, storeDir, id); !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("card still there: %v", err)
	}
}

func TestIssueDeleteUnknownCard(t *testing.T) {
	storeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(storeDir, "dispatcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := RunIssueDelete(&Printer{W: &bytes.Buffer{}}, IssueDeleteOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         "deadbeef",
		Yes:                true,
	})
	if err == nil {
		t.Fatal("deleting an unknown card must fail")
	}
}

func TestIssueUpdateSetsBotAndMergesBotArgs(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{
		Title:   "route me",
		Bot:     "old-bot",
		BotArgs: map[string]string{"keep": "1", "replace": "old", "drop": "x"},
	})
	bot := "docs-refresh"
	if err := RunIssueUpdate(&Printer{W: &bytes.Buffer{}}, IssueUpdateOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         id,
		Bot:                &bot,
		BotArgs:            []string{"replace=new", "globs=README.md,docs/**/*.md"},
		ClearBotArg:        []string{"drop"},
	}); err != nil {
		t.Fatalf("RunIssueUpdate: %v", err)
	}
	got, err := reloadIssue(t, storeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bot != "docs-refresh" {
		t.Errorf("Bot = %q", got.Bot)
	}
	want := map[string]string{"keep": "1", "replace": "new", "globs": "README.md,docs/**/*.md"}
	if len(got.BotArgs) != len(want) {
		t.Fatalf("BotArgs = %v, want %v", got.BotArgs, want)
	}
	for k, v := range want {
		if got.BotArgs[k] != v {
			t.Errorf("BotArgs[%s] = %q, want %q", k, got.BotArgs[k], v)
		}
	}
}

func TestIssueUpdateEmptyBotClearsIt(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "t", Bot: "some-bot", BotArgs: map[string]string{"a": "1"}})
	empty := ""
	if err := RunIssueUpdate(&Printer{W: &bytes.Buffer{}}, IssueUpdateOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         id,
		Bot:                &empty,
	}); err != nil {
		t.Fatalf("RunIssueUpdate: %v", err)
	}
	got, err := reloadIssue(t, storeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bot != "" {
		t.Errorf("Bot = %q, want cleared", got.Bot)
	}
	if got.BotArgs["a"] != "1" {
		t.Errorf("an untouched bot-arg set must survive: %v", got.BotArgs)
	}
}

func TestIssueUpdateBotArgRejectsMalformedPair(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "t"})
	err := RunIssueUpdate(&Printer{W: &bytes.Buffer{}}, IssueUpdateOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         id,
		BotArgs:            []string{"no-equals-sign"},
	})
	if err == nil {
		t.Fatal("a --bot-arg without key=value must be refused")
	}
}

func TestIssueUpdateBotArgDropsATypedGrant(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "t"})
	if err := RunIssueUpdate(&Printer{W: &bytes.Buffer{}}, IssueUpdateOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         id,
		BotArgs:            []string{"loop_cap=5", "forge_publish_token=" + cliGrantSentinel},
	}); err != nil {
		t.Fatalf("RunIssueUpdate: %v", err)
	}
	got, err := reloadIssue(t, storeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range got.BotArgs {
		if strings.Contains(v, cliGrantSentinel) {
			t.Errorf("bot arg %s carries the typed grant", k)
		}
	}
	if got.BotArgs["loop_cap"] != "5" {
		t.Errorf("the operator's own arg was lost: %v", got.BotArgs)
	}
}

func TestIssueDeleteRefusesAnEmptyReference(t *testing.T) {
	storeDir, id := seedIssueCard(t, native.Issue{Title: "only card"})
	err := RunIssueDelete(&Printer{W: &bytes.Buffer{}}, IssueDeleteOptions{
		IssueCommonOptions: IssueCommonOptions{StoreDir: storeDir},
		IDOrPrefix:         "  ",
		Yes:                true,
	})
	if err == nil {
		t.Fatal("an empty id must be refused")
	}
	if _, gerr := reloadIssue(t, storeDir, id); gerr != nil {
		t.Fatalf("the only card was deleted by an empty reference: %v", gerr)
	}
}
