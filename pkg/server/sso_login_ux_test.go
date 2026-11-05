package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/auth/oidc"
	"github.com/arketov/iterion/pkg/auth/orgsso"
)

// TestListProviders_DiscoverByEmailDomain proves the login screen can surface an
// org's SSO from the user's email domain alone — no org slug required.
func TestListProviders_DiscoverByEmailDomain(t *testing.T) {
	s := newOrgSSOTestServer(t)
	ctx := context.Background()
	domains := orgsso.NewMemoryDomainStore()
	s.orgDomains = domains
	verified := time.Now()
	if err := domains.Create(ctx, orgsso.VerifiedDomain{
		ID: "d1", TenantID: "t1", Domain: "acme.example", Token: "tok", VerifiedAt: &verified, CreatedAt: verified,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.orgSSO.Create(ctx, orgsso.OrgSSOProvider{
		ID: "p1", TenantID: "t1", Kind: orgsso.KindOIDC, Enabled: true,
		IssuerURL: "https://sso.acme.example/realms/main", ClientID: "iterion",
		DisplayName: "Acme KC", CreatedAt: verified,
	}); err != nil {
		t.Fatal(err)
	}

	get := func(q string) []map[string]string {
		w := httptest.NewRecorder()
		s.handleListProviders(w, httptest.NewRequest("GET", "/api/auth/providers"+q, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("providers%s: code=%d", q, w.Code)
		}
		var out struct {
			Providers []map[string]string `json:"providers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Providers
	}

	// By email domain.
	if ps := get("?email=alice@acme.example"); len(ps) != 1 || ps[0]["name"] != "oidc-org-p1" {
		t.Fatalf("by email = %v, want one oidc-org-p1", ps)
	}
	// By explicit domain.
	if ps := get("?domain=acme.example"); len(ps) != 1 {
		t.Fatalf("by domain = %v, want one", ps)
	}
	// Unknown domain → no org provider (non-oracle), never an error.
	if ps := get("?email=nobody@unknown.example"); len(ps) != 0 {
		t.Fatalf("unknown domain leaked providers: %v", ps)
	}
}

// TestOIDCCallback_UnknownProviderRedirectsToSPA proves a callback failure lands
// on the SPA login screen with a stable sso_error code, not a raw API page.
func TestOIDCCallback_UnknownProviderRedirectsToSPA(t *testing.T) {
	s := &Server{cfg: Config{}}
	s.oidcStates = oidc.NewMemoryStateStore(time.Minute)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/auth/oidc/nope/callback?state=x&code=y", nil)
	r.SetPathValue("provider", "nope")
	s.handleOIDCCallback(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if want := "/login?sso_error=" + ssoErrUnknownProvider; loc != want {
		t.Fatalf("Location = %q, want %q", loc, want)
	}
}

// TestOIDCCallback_StateExpiredRedirectsToSPA proves an expired/missing state
// redirects with the state_expired code instead of a 400 page.
func TestOIDCCallback_StateExpiredRedirectsToSPA(t *testing.T) {
	s := newOrgSSOTestServer(t)
	s.oidcStates = oidc.NewMemoryStateStore(time.Minute)
	// Register an enabled org provider (with a sealed secret) so resolveConnector
	// succeeds and we reach the state lookup (no entry → state_expired).
	sealed, err := orgsso.SealClientSecret(s.sealer, "p1", "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.orgSSO.Create(context.Background(), orgsso.OrgSSOProvider{
		ID: "p1", TenantID: "t1", Kind: orgsso.KindOIDC, Enabled: true,
		IssuerURL: "https://sso.acme.example/realms/main", ClientID: "iterion",
		SealedSecret: sealed, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/auth/oidc/oidc-org-p1/callback?state=missing&code=y", nil)
	r.SetPathValue("provider", "oidc-org-p1")
	s.handleOIDCCallback(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login?") ||
		!strings.Contains(loc, "sso_error="+ssoErrStateExpired) {
		t.Fatalf("Location = %q, want /login?...sso_error=%s", loc, ssoErrStateExpired)
	}
}
