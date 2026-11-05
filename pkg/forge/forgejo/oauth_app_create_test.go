package forgejo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arketov/iterion/pkg/forge"
)

func TestCreateOAuthApp(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/user/applications/oauth2" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "token usertok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3, "client_id": "cid-fj", "client_secret": "sec-fj"})
	}))
	defer srv.Close()

	c := New(srv.Client(), srv.URL, "usertok")
	creds, err := c.CreateOAuthApp(context.Background(), forge.OAuthAppSpec{
		Name: "iterion", RedirectURI: "https://it/cb", Confidential: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.ClientID != "cid-fj" || creds.ClientSecret != "sec-fj" || creds.ProviderAppID != "3" {
		t.Fatalf("creds = %+v", creds)
	}
	uris, _ := gotBody["redirect_uris"].([]any)
	if len(uris) != 1 || uris[0] != "https://it/cb" || gotBody["confidential_client"] != true {
		t.Fatalf("request body = %+v", gotBody)
	}
}
