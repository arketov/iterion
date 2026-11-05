package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/secrets"
)

func newBindingTestServer(t *testing.T) *Server {
	t.Helper()
	s := newOrgTestServer(t)
	s.botBindings = secrets.NewMemoryBotSecretBindingStore()
	s.genericSecrets = secrets.NewMemoryGenericSecretStore()
	return s
}

func bindReq(ctx context.Context, method, path, body, teamID, botID, bindingID string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r = r.WithContext(ctx)
	if teamID != "" {
		r.SetPathValue("id", teamID)
	}
	if botID != "" {
		r.SetPathValue("bot_id", botID)
	}
	if bindingID != "" {
		r.SetPathValue("binding_id", bindingID)
	}
	return r
}

func TestBotBinding_CRUD(t *testing.T) {
	s := newBindingTestServer(t)
	ctx := superAdminCtx()
	const base = "/api/teams/t1/bots/review-pr/bindings"
	if err := s.genericSecrets.Create(ctx, secrets.GenericSecret{ID: "sec1", ScopeTeamID: "t1", Name: "org_gitlab", SealedSecret: []byte("x"), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// unknown secret_id → 400
	w := httptest.NewRecorder()
	s.handleCreateBotBinding(w, bindReq(ctx, "POST", base, `{"secret_id":"ghost","secret_name_for_workflow":"gitlab_token"}`, "t1", "review-pr", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown secret: code=%d body=%s", w.Code, w.Body.String())
	}

	// valid create → 201
	w = httptest.NewRecorder()
	s.handleCreateBotBinding(w, bindReq(ctx, "POST", base, `{"secret_id":"sec1","secret_name_for_workflow":"gitlab_token","allowed_hosts":["gitlab.com"]}`, "t1", "review-pr", ""))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", w.Code, w.Body.String())
	}
	var created secrets.BotSecretBinding
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.SecretNameForWorkflow != "gitlab_token" || created.BotID != "review-pr" || created.TenantID != "t1" {
		t.Fatalf("bad binding: %+v", created)
	}

	// list → 1
	w = httptest.NewRecorder()
	s.handleListBotBindings(w, bindReq(ctx, "GET", base, "", "t1", "review-pr", ""))
	var lr struct {
		Bindings []secrets.BotSecretBinding `json:"bindings"`
	}
	json.Unmarshal(w.Body.Bytes(), &lr)
	if len(lr.Bindings) != 1 {
		t.Fatalf("list: %d", len(lr.Bindings))
	}

	// update under a different bot → 404 (ownership check)
	w = httptest.NewRecorder()
	s.handleUpdateBotBinding(w, bindReq(ctx, "PATCH", base, `{"allowed_hosts":["x.com"]}`, "t1", "other-bot", created.ID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-bot update should 404: code=%d", w.Code)
	}

	// update ok
	w = httptest.NewRecorder()
	s.handleUpdateBotBinding(w, bindReq(ctx, "PATCH", base, `{"allowed_hosts":["only.com"]}`, "t1", "review-pr", created.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("update: code=%d body=%s", w.Code, w.Body.String())
	}

	// delete → 204
	w = httptest.NewRecorder()
	s.handleDeleteBotBinding(w, bindReq(ctx, "DELETE", base, "", "t1", "review-pr", created.ID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: code=%d", w.Code)
	}
	// gone
	w = httptest.NewRecorder()
	s.handleListBotBindings(w, bindReq(ctx, "GET", base, "", "t1", "review-pr", ""))
	json.Unmarshal(w.Body.Bytes(), &lr)
	if len(lr.Bindings) != 0 {
		t.Fatalf("after delete: %d", len(lr.Bindings))
	}
}
