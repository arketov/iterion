package trigger

import (
	"context"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// A card moved from one bot to another keeps its own bot-args: the new bot's
// subscription vars land over them, a key the subscription does not set
// survives. Wiping them made the merger lose feature_branch & co. and the
// lead treat a returned card as new.
func TestNativeBoardPromoteMergesBotArgsOnBotChange(t *testing.T) {
	ns, err := native.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	stored, err := ns.Create(native.Issue{
		Title: "feature card", State: native.StateReady, Bot: "lead",
		BotArgs: map[string]string{"feature_branch": "x", "a": "1", "shared": "old"},
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	plan := LaunchPlan{
		BotID: "merger",
		Vars:  map[string]string{"issue_id": stored.ID, "shared": "new"},
		Event: Event{Subject: Subject{Type: "card", ID: stored.ID}},
	}
	if _, err := NewNativeBoardEffect(ns, nil, nil).Promote(context.Background(), plan); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	after, err := ns.Get(stored.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if after.Bot != "merger" {
		t.Fatalf("the promotion did not pin its bot (%q)", after.Bot)
	}
	for k, want := range map[string]string{
		"feature_branch": "x",
		"a":              "1",
		"issue_id":       stored.ID,
		"shared":         "new",
	} {
		if got := after.BotArgs[k]; got != want {
			t.Errorf("bot_args[%s] = %q, want %q (all: %v)", k, got, want, after.BotArgs)
		}
	}
}

// A card already pinned to the subscription's bot is not touched: its args
// stay exactly as they were (no churn, no vars overlay).
func TestNativeBoardPromoteLeavesAnAlreadyPinnedCardUntouched(t *testing.T) {
	ns, err := native.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	stored, err := ns.Create(native.Issue{
		Title: "pinned card", State: native.StateReady, Bot: "merger",
		BotArgs: map[string]string{"feature_branch": "x"},
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	plan := LaunchPlan{
		BotID: "merger",
		Vars:  map[string]string{"extra": "y"},
		Event: Event{Subject: Subject{Type: "card", ID: stored.ID}},
	}
	if _, err := NewNativeBoardEffect(ns, nil, nil).Promote(context.Background(), plan); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	after, err := ns.Get(stored.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if len(after.BotArgs) != 1 || after.BotArgs["feature_branch"] != "x" {
		t.Errorf("an already-pinned card's bot_args changed: %v", after.BotArgs)
	}
}

// A promotion MERGES bot-args on a card that may be webhook-carried: the
// card's own args stay, the subscription's vars land over them, and the
// card's SERVER-stamped launch context survives untouched. A promotion
// wiping it launches the card's run without the identity its quota reads,
// and silently degrades the webhook's BYOK resolution — the local effect
// and the cloud twin (pkg/server trigger_cloud.go) share the semantics,
// each pinned.
func TestNativeBoardPromoteKeepsTheStoredLaunchContext(t *testing.T) {
	ns, err := native.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("native.NewStore: %v", err)
	}
	stored, err := ns.Create(native.Issue{
		Title: "webhook card", State: native.StateReady, Bot: "botA",
		BotArgs: map[string]string{
			"member_var":              "v1",
			"__iterion_repo_url":      "https://github.com/acme/widgets.git",
			"__iterion_project_path":  "acme/widgets",
			"__iterion_webhook_id":    "wh-t1",
			"__iterion_key_overrides": `{"https://github.com": {"token": "k1"}}`,
		},
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	plan := LaunchPlan{
		BotID:    "botB",
		TenantID: "t1",
		Vars: map[string]string{
			"team_var":               "x",
			"__iterion_project_path": "harmless/other",
			"__iterion_webhook_id":   "wh-x",
		},
		Event: Event{TenantID: "t1", Subject: Subject{Type: "card", ID: stored.ID}},
	}
	if _, err := NewNativeBoardEffect(ns, nil, nil).Promote(context.Background(), plan); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	after, err := ns.Get(stored.ID)
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if after.Bot != "botB" {
		t.Fatalf("the promotion did not pin its bot (%q) — this test only means something while the stamp lands", after.Bot)
	}
	if after.BotArgs["team_var"] != "x" {
		t.Errorf("the subscription's own var did not land: %v", after.BotArgs)
	}
	if after.BotArgs["member_var"] != "v1" {
		t.Errorf("a promotion must keep the card's own member vars the subscription does not set: %v", after.BotArgs)
	}
	for k, want := range map[string]string{
		"__iterion_repo_url":      "https://github.com/acme/widgets.git",
		"__iterion_project_path":  "acme/widgets",
		"__iterion_webhook_id":    "wh-t1",
		"__iterion_key_overrides": `{"https://github.com": {"token": "k1"}}`,
	} {
		if got := after.BotArgs[k]; got != want {
			t.Errorf("the promotion wiped the server-stamped %s to %q, want %q — the card's run loses its meter, bound and BYOK audience", k, got, want)
		}
	}
}
