package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/forge"
)

func TestAuthorizeURL(t *testing.T) {
	a := &OAuthApp{BaseURL: "https://gitlab.example.com", ClientID: "cid"}
	got := a.AuthorizeURL("https://iterion/cb", "st8", "chal", nil)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/oauth/authorize" {
		t.Errorf("path = %q", u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("state") != "st8" || q.Get("response_type") != "code" {
		t.Errorf("query = %v", q)
	}
	if q.Get("scope") != "api" {
		t.Errorf("default scope = %q, want api", q.Get("scope"))
	}
	if q.Get("code_challenge") != "chal" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("pkce params missing: %v", q)
	}
}

func TestExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "abc" {
			t.Errorf("form = %v", r.Form)
		}
		if r.Form.Get("code_verifier") != "verif" {
			t.Errorf("code_verifier = %q", r.Form.Get("code_verifier"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":7200,"scope":"api"}`))
	}))
	defer srv.Close()

	a := &OAuthApp{HTTP: srv.Client(), BaseURL: srv.URL, ClientID: "cid", ClientSecret: "sec"}
	tok, err := a.Exchange(context.Background(), "abc", "https://iterion/cb", "verif")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Errorf("tokens = %+v", tok)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("expiry not computed from expires_in")
	}
	if len(tok.Scopes) != 1 || tok.Scopes[0] != "api" {
		t.Errorf("scopes = %v", tok.Scopes)
	}
}

func TestRefresh_InvalidGrantIsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	a := &OAuthApp{HTTP: srv.Client(), BaseURL: srv.URL, ClientID: "cid", ClientSecret: "sec"}
	_, err := a.Refresh(context.Background(), forge.Connection{}, "stale-refresh")
	if !errors.Is(err, forge.ErrUnauthorized) {
		t.Errorf("invalid_grant refresh = %v, want ErrUnauthorized", err)
	}
}

func TestRefresh_EmptyTokenIsUnauthorized(t *testing.T) {
	a := &OAuthApp{BaseURL: "https://gl"}
	_, err := a.Refresh(context.Background(), forge.Connection{}, "  ")
	if !errors.Is(err, forge.ErrUnauthorized) {
		t.Errorf("empty refresh = %v, want ErrUnauthorized", err)
	}
}

func TestExchange_ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer srv.Close()
	a := &OAuthApp{HTTP: srv.Client(), BaseURL: srv.URL}
	_, err := a.Exchange(context.Background(), "x", "y", "")
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Errorf("err = %v, want invalid_request", err)
	}
}
