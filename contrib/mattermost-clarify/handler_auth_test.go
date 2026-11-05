package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/notify"
)

// postJSON builds a POST request to a handler and returns the recorder.
func postJSON(t *testing.T, h http.HandlerFunc, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCompletionHandler_ValidSignatureAccepted(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	secret := "topsecret"
	token := encodeToken(callbackToken{ChannelID: "c1", RootID: "root1"})
	body, _ := json.Marshal(notify.CompletionPayload{
		Status: "finished", FinalAnswer: "ready?", CallbackToken: token,
	})

	rec := postJSON(t, completionHandler(context.Background(), co, secret), string(body),
		map[string]string{notify.SignatureHeader: notify.Sign(secret, body)})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(d.replies) != 1 || d.replies[0] != "ready?" {
		t.Fatalf("expected reply posted, got %+v", d.replies)
	}
}

func TestCompletionHandler_BadSignatureRejected(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	token := encodeToken(callbackToken{ChannelID: "c1", RootID: "root1"})
	body, _ := json.Marshal(notify.CompletionPayload{
		Status: "finished", FinalAnswer: "leak", CallbackToken: token,
	})

	// Signed with the WRONG secret — must be rejected, nothing posted.
	rec := postJSON(t, completionHandler(context.Background(), co, "realsecret"), string(body),
		map[string]string{notify.SignatureHeader: notify.Sign("forger", body)})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(d.replies) != 0 {
		t.Fatalf("forged callback must not post; got %+v", d.replies)
	}
}

func TestCompletionHandler_MissingSignatureRejectedWhenSecretSet(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	body, _ := json.Marshal(notify.CompletionPayload{Status: "finished", FinalAnswer: "x"})

	rec := postJSON(t, completionHandler(context.Background(), co, "secret"), string(body), nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no signature)", rec.Code)
	}
}

func TestCompletionHandler_NoSecretSkipsVerification(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	token := encodeToken(callbackToken{ChannelID: "c1", RootID: "root1"})
	body, _ := json.Marshal(notify.CompletionPayload{
		Status: "finished", FinalAnswer: "hi", CallbackToken: token,
	})

	// secret == "" → unauthenticated mode; unsigned request accepted.
	rec := postJSON(t, completionHandler(context.Background(), co, ""), string(body), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(d.replies) != 1 {
		t.Fatalf("expected reply in no-secret mode, got %+v", d.replies)
	}
}

func TestActionHandler_ValidTokenSetsConsent(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	body := `{"context":{"action":"consent","granted":true,"user_id":"u1","channel_id":"c1","root_id":"root1","token":"good"}}`

	rec := postJSON(t, actionHandler(co, "good"), body, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !co.store.HasConsented("root1", "u1") {
		t.Error("valid consent click should have granted consent")
	}
}

func TestActionHandler_BadTokenRejected(t *testing.T) {
	d := &fakeDriver{}
	co := NewCoordinator(d, alwaysFilter{}, &fakeLauncher{})
	body := `{"context":{"action":"consent","granted":true,"user_id":"u1","channel_id":"c1","root_id":"root1","token":"wrong"}}`

	rec := postJSON(t, actionHandler(co, "good"), body, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if co.store.HasConsented("root1", "u1") {
		t.Error("forged consent click must NOT grant consent")
	}
}
