package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arketov/iterion/pkg/forge"
)

func TestCreateOAuthApp(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/applications" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer admintok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "application_id": "cid-auto", "secret": "sec-auto"})
	}))
	defer srv.Close()

	c := New(srv.Client(), srv.URL, "admintok")
	creds, err := c.CreateOAuthApp(context.Background(), forge.OAuthAppSpec{
		Name: "iterion", RedirectURI: "https://it/cb", Scopes: []string{"api"}, Confidential: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.ClientID != "cid-auto" || creds.ClientSecret != "sec-auto" || creds.ProviderAppID != "7" {
		t.Fatalf("creds = %+v", creds)
	}
	if gotBody["redirect_uri"] != "https://it/cb" || gotBody["scopes"] != "api" || gotBody["confidential"] != true {
		t.Fatalf("request body = %+v", gotBody)
	}
}

func TestCreateOAuthApp_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.Client(), srv.URL, "nonadmin")
	if _, err := c.CreateOAuthApp(context.Background(), forge.OAuthAppSpec{Name: "x"}); !errors.Is(err, forge.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
