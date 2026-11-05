package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/arketov/iterion/pkg/notify"
)

// fakeDriver records consent prompts and replies, and lets a test feed
// posts.
type fakeDriver struct {
	mu       sync.Mutex
	consents []ConsentAction // prompts requested (Granted unused)
	replies  []string
}

func (f *fakeDriver) Listen(ctx context.Context) (<-chan InboundPost, error) {
	ch := make(chan InboundPost)
	close(ch)
	return ch, nil
}
func (f *fakeDriver) RequestConsent(_ context.Context, t ThreadRef, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consents = append(f.consents, ConsentAction{Thread: t, UserID: userID})
	return nil
}
func (f *fakeDriver) PostReply(_ context.Context, _ ThreadRef, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, text)
	return nil
}

// fakeLauncher records launches and returns a canned transcript.
type fakeLauncher struct {
	mu          sync.Mutex
	transcripts []string
	tokens      []string
}

func (f *fakeLauncher) Launch(_ context.Context, transcript, _ /*latest*/, _ /*threadID*/, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transcripts = append(f.transcripts, transcript)
	f.tokens = append(f.tokens, token)
	return "run-x", nil
}

// alwaysFilter forces a relevance hit so launch logic is exercised.
type alwaysFilter struct{}

func (alwaysFilter) ShouldRespond(_, _ string) bool { return true }

func thread() ThreadRef { return ThreadRef{ChannelID: "c1", RootID: "root1"} }

func TestCoordinator_IgnoresUntilActivated(t *testing.T) {
	d := &fakeDriver{}
	l := &fakeLauncher{}
	co := NewCoordinator(d, alwaysFilter{}, l)

	// No mention yet → thread inactive → nothing happens.
	co.HandlePost(context.Background(), InboundPost{
		Thread: thread(), UserID: "u1", Text: "what do you mean?", CreateAtMillis: 1,
	})
	if len(l.transcripts) != 0 {
		t.Fatal("must not launch on an inactive thread")
	}
	if len(d.consents) != 0 {
		t.Fatal("must not prompt on an inactive thread")
	}
}

func TestCoordinator_MentionActivatesAndPrompts(t *testing.T) {
	d := &fakeDriver{}
	l := &fakeLauncher{}
	co := NewCoordinator(d, alwaysFilter{}, l)

	co.HandlePost(context.Background(), InboundPost{
		Thread: thread(), UserID: "u1", Text: "@clarify-bot help", CreateAtMillis: 1, MentionsBot: true,
	})
	if len(d.consents) != 1 || d.consents[0].UserID != "u1" {
		t.Fatalf("expected one consent prompt to u1, got %+v", d.consents)
	}
	// Not consented yet → no launch even though filter says yes.
	if len(l.transcripts) != 0 {
		t.Fatal("must not launch before consent")
	}
}

func TestCoordinator_LaunchesOnlyAfterConsent(t *testing.T) {
	d := &fakeDriver{}
	l := &fakeLauncher{}
	co := NewCoordinator(d, alwaysFilter{}, l)
	ctx := context.Background()

	// Activate.
	co.HandlePost(ctx, InboundPost{Thread: thread(), UserID: "u1", Text: "@clarify-bot", CreateAtMillis: 1, MentionsBot: true})
	// Consent.
	co.HandleConsent(ConsentAction{Thread: thread(), UserID: "u1", Granted: true})
	// Now a relevant message launches.
	co.HandlePost(ctx, InboundPost{Thread: thread(), UserID: "u1", Text: "what do you mean by that?", CreateAtMillis: 2})

	if len(l.transcripts) != 1 {
		t.Fatalf("expected one launch after consent, got %d", len(l.transcripts))
	}
	if !strings.Contains(l.transcripts[0], "User A: what do you mean by that?") {
		t.Errorf("transcript missing consenting message: %q", l.transcripts[0])
	}
}

func TestCoordinator_NonConsentingExcludedFromTranscript(t *testing.T) {
	d := &fakeDriver{}
	l := &fakeLauncher{}
	co := NewCoordinator(d, alwaysFilter{}, l)
	ctx := context.Background()

	co.HandlePost(ctx, InboundPost{Thread: thread(), UserID: "u1", Text: "@clarify-bot", CreateAtMillis: 1, MentionsBot: true})
	co.HandleConsent(ConsentAction{Thread: thread(), UserID: "u1", Granted: true})

	// u2 speaks but never consents — their text must never reach the LLM.
	co.HandlePost(ctx, InboundPost{Thread: thread(), UserID: "u2", Text: "private detail xyz", CreateAtMillis: 2})
	// u1 (consenting) triggers a launch.
	co.HandlePost(ctx, InboundPost{Thread: thread(), UserID: "u1", Text: "what did u2 mean?", CreateAtMillis: 3})

	if len(l.transcripts) != 1 {
		t.Fatalf("expected one launch, got %d", len(l.transcripts))
	}
	if strings.Contains(l.transcripts[0], "private detail xyz") {
		t.Errorf("non-consenting user's content leaked: %q", l.transcripts[0])
	}
}

func TestCoordinator_HandleCompletionPostsReply(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	token := encodeToken(callbackToken{ChannelID: "c1", RootID: "root1"})

	err := co.HandleCompletion(context.Background(), notify.CompletionPayload{
		Status: "finished", FinalAnswer: "Did you mean staging or prod?", CallbackToken: token,
	})
	if err != nil {
		t.Fatalf("HandleCompletion: %v", err)
	}
	if len(d.replies) != 1 || d.replies[0] != "Did you mean staging or prod?" {
		t.Fatalf("expected reply posted, got %+v", d.replies)
	}
}

func TestCoordinator_EmptyAnswerPostsNothing(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	token := encodeToken(callbackToken{ChannelID: "c1", RootID: "root1"})

	if err := co.HandleCompletion(context.Background(), notify.CompletionPayload{
		Status: "finished", FinalAnswer: "", CallbackToken: token,
	}); err != nil {
		t.Fatalf("HandleCompletion: %v", err)
	}
	if len(d.replies) != 0 {
		t.Fatalf("empty answer must post nothing, got %+v", d.replies)
	}
}
