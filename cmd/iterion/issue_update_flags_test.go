package main

import (
	"path/filepath"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// The flags of `issue update` must reach the store through the cobra command,
// not only through cli.RunIssueUpdate (which the pkg/cli tests call directly).
func TestIssueUpdateCommandWiresBotFlags(t *testing.T) {
	storeDir := t.TempDir()
	root := filepath.Join(storeDir, "dispatcher")
	seed, err := native.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	iss, err := seed.Create(native.Issue{Title: "route me", BotArgs: map[string]string{"drop": "x", "keep": "1"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = seed.Close()

	t.Cleanup(func() {
		issueUpdateBot, issueUpdateBotArgs, issueUpdateClearBotArg = "", nil, nil
		for _, n := range []string{"bot", "bot-arg", "clear-bot-arg"} {
			if f := issueUpdateCmd.Flags().Lookup(n); f != nil {
				f.Changed = false
			}
		}
		issueStoreDir = ""
	})
	issueStoreDir = storeDir
	for flag, val := range map[string]string{"bot": "docs-refresh", "bot-arg": "a=b,c", "clear-bot-arg": "drop"} {
		if err := issueUpdateCmd.Flags().Set(flag, val); err != nil {
			t.Fatalf("set --%s: %v", flag, err)
		}
	}
	if err := issueUpdateCmd.RunE(issueUpdateCmd, []string{iss.ID}); err != nil {
		t.Fatalf("issue update: %v", err)
	}

	reopened, err := native.NewStore(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.Get(iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bot != "docs-refresh" {
		t.Errorf("--bot did not reach the card: %q", got.Bot)
	}
	if got.BotArgs["a"] != "b,c" || got.BotArgs["keep"] != "1" {
		t.Errorf("--bot-arg merge wrong: %v", got.BotArgs)
	}
	if _, still := got.BotArgs["drop"]; still {
		t.Errorf("--clear-bot-arg did not remove the key: %v", got.BotArgs)
	}
}
