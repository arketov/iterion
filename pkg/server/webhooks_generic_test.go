package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/webhooks"
)

func genConfig() webhooks.Config {
	return webhooks.Config{
		ID: "gnw", TenantID: "t1", Provider: webhooks.ProviderGeneric,
		Enabled: true, BotIDs: []string{"review-pr", "feature_dev"},
	}
}

func genReq(ctx context.Context, body string) *http.Request {
	r := httptest.NewRequest("POST", "/api/webhooks/generic/gnw", strings.NewReader(body)).WithContext(ctx)
	r.SetPathValue("id", "gnw")
	return r
}

func TestGenericWebhook_HappyPath_VarsPrecedence(t *testing.T) {
	s := newWebhookTestServer(t)
	var gotVars map[string]string
	var gotBot, gotURL, gotRef string
	s.webhookLaunchBot = func(_ context.Context, botID string, vars map[string]string, repoURL, repoRef, projectPath string, _, _ map[string]string) (string, error) {
		gotBot, gotVars, gotURL, gotRef = botID, vars, repoURL, repoRef
		return "run-g-1", nil
	}
	cfg := genConfig()
	// Config-pinned var must override the request-supplied one.
	cfg.LaunchVars = map[string]string{"severity": "high"}

	body := `{
	  "bot": "review-pr",
	  "vars": {"severity": "low", "scope_notes": "do thing"},
	  "idempotency_key": "evt-1",
	  "repo_url": "https://example.com/x.git",
	  "repo_ref": "main"
	}`
	w := httptest.NewRecorder()
	s.handleGenericWebhook(w, genReq(gitlabCtx(cfg), body))
	if w.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if gotBot != "review-pr" || gotURL != "https://example.com/x.git" || gotRef != "main" {
		t.Fatalf("dispatch: bot=%q url=%q ref=%q", gotBot, gotURL, gotRef)
	}
	if gotVars["severity"] != "high" { // config WINS
		t.Fatalf("config var must override request: %v", gotVars)
	}
	if gotVars["scope_notes"] != "do thing" {
		t.Fatalf("request var not threaded: %v", gotVars)
	}
}

func TestGenericWebhook_MissingBotIs400(t *testing.T) {
	s := newWebhookTestServer(t)
	s.webhookLaunchBot = func(context.Context, string, map[string]string, string, string, string, map[string]string, map[string]string) (string, error) {
		t.Fatal("must not launch")
		return "", nil
	}
	cfg := genConfig()
	// Strip the single-bot scope to force the "no default" branch.
	cfg.BotIDs = []string{"a", "b"}
	w := httptest.NewRecorder()
	s.handleGenericWebhook(w, genReq(gitlabCtx(cfg), `{}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing bot: %d body=%s", w.Code, w.Body.String())
	}
}

func TestGenericWebhook_BodyHashIdempotency(t *testing.T) {
	s := newWebhookTestServer(t)
	var calls int
	s.webhookLaunchBot = func(context.Context, string, map[string]string, string, string, string, map[string]string, map[string]string) (string, error) {
		calls++
		return "run-g-2", nil
	}
	cfg := genConfig()
	// No idempotency_key in body → handler falls back to sha256(body).
	body := `{"bot":"review-pr","vars":{}}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.handleGenericWebhook(w, genReq(gitlabCtx(cfg), body))
		if i == 0 && w.Code != http.StatusAccepted {
			t.Fatalf("first: %d", w.Code)
		}
		if i == 1 {
			if w.Code != http.StatusOK {
				t.Fatalf("replay: %d body=%s", w.Code, w.Body.String())
			}
			var resp map[string]string
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["status"] != webhooks.StatusDuplicate {
				t.Fatalf("expected duplicate: %v", resp)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("expected one launch, got %d", calls)
	}
}

func TestGenericWebhook_OversizedVarRejected(t *testing.T) {
	s := newWebhookTestServer(t)
	s.webhookLaunchBot = func(context.Context, string, map[string]string, string, string, string, map[string]string, map[string]string) (string, error) {
		t.Fatal("must not launch")
		return "", nil
	}
	cfg := genConfig()
	big := strings.Repeat("x", 4097)
	body := `{"bot":"review-pr","vars":{"k":"` + big + `"}}`
	w := httptest.NewRecorder()
	s.handleGenericWebhook(w, genReq(gitlabCtx(cfg), body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized var: %d", w.Code)
	}
}
