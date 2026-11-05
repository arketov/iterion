package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/arketov/iterion/pkg/secure/httpdial"
)

// GenericConnector implements Connector against any OpenID Connect-
// compliant provider via the discovery document. It fetches the
// .well-known/openid-configuration on first use and caches it for
// discoveryTTL (so a per-flow connector pays discovery once, and a
// long-lived deployment connector refreshes endpoints/keys instead of
// caching them forever across an IdP rotation).
//
// We do not validate the ID token signature here: the userinfo
// endpoint is authenticated with the just-minted access token, and
// the token endpoint already authenticated the request via
// client_secret_basic. JWKS-backed ID-token verification is a planned
// hardening (the prerequisite for safe auto-link) — see docs/cloud-admin.md.
//
// SSRF: when constructed for a per-org (org-admin-supplied) issuer, the
// connector is handed an httpdial.SafeClient whose transport pins every dial
// to a validated public-unicast IP — so discovery AND the token/userinfo
// endpoints discovered from the doc are all guarded (second-order SSRF
// closed). When strict, the discovered endpoints are additionally required to
// be https and the doc's issuer must match the configured issuer.
type GenericConnector struct {
	name         string
	display      string
	issuerURL    string
	clientID     string
	clientSecret string
	scopes       []string
	httpClient   *http.Client
	// strict gates the extra OIDC-doc validation (https endpoints) applied to
	// org-admin-supplied issuers, and makes ID-token verification mandatory
	// (reject a token response that omits the id_token). The issuer-match +
	// (when an id_token IS present) signature checks run regardless.
	strict bool

	// jwks caches the issuer's signing keys for ID-token verification, built
	// lazily from the discovered jwks_uri (guarded by docMu for the pointer
	// init; the cache itself is concurrency-safe).
	jwks *jwksCache

	// docMu guards doc, docOK, discoveredAt — and serialises discovery
	// attempts so concurrent SSO starts only fire one HTTP request at a
	// time. We cache on success for discoveryTTL; an error is never cached
	// (so a transient boot-time failure cannot brick the connector).
	docMu        sync.Mutex
	doc          discoveryDoc
	docOK        bool
	discoveredAt time.Time
}

type discoveryDoc struct {
	Issuer       string `json:"issuer"`
	AuthorizeURL string `json:"authorization_endpoint"`
	TokenURL     string `json:"token_endpoint"`
	UserInfoURL  string `json:"userinfo_endpoint"`
	JWKSURL      string `json:"jwks_uri"`
}

// discoveryTTL caps how long a successful discovery doc is reused. Bounds
// staleness after an IdP rotates endpoints/keys without requiring a restart.
const discoveryTTL = time.Hour

// maxOIDCBodyBytes caps the bytes read from any IdP response (discovery,
// token, userinfo) — a malicious or broken IdP cannot exhaust memory.
const maxOIDCBodyBytes = 1 << 20 // 1 MiB

// NewGenericConnector returns a discovery-based connector under the fixed slug
// "sso" using a plain HTTP client (back-compat: the deployment-global,
// operator-configured generic provider). Per-org Keycloak rows use
// NewGenericConnectorWithSlug with an SSRF-guarded client.
func NewGenericConnector(issuerURL, clientID, clientSecret, display string, scopes []string) *GenericConnector {
	return NewGenericConnectorWithSlug("sso", issuerURL, clientID, clientSecret, display, scopes, nil, false)
}

// NewGenericConnectorWithSlug returns a discovery-based connector under the
// supplied slug. client, when non-nil, overrides the default HTTP client — the
// server passes httpdial.SafeClient(strict, …) for per-org issuers so every
// dial is SSRF-guarded. strict additionally requires discovered endpoints to
// be https (the issuer-match check runs regardless of strict).
func NewGenericConnectorWithSlug(slug, issuerURL, clientID, clientSecret, display string, scopes []string, client *http.Client, strict bool) *GenericConnector {
	if display == "" {
		display = "SSO"
	}
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GenericConnector{
		name:         slug,
		display:      display,
		issuerURL:    strings.TrimRight(issuerURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		scopes:       scopes,
		httpClient:   client,
		strict:       strict,
	}
}

// SafeGenericClient builds the SSRF-guarded HTTP client a per-org connector
// should use. Exposed so the server can construct the client at resolve time
// without importing the transport details.
func SafeGenericClient(strict bool) *http.Client {
	return httpdial.SafeClient(strict, 10*time.Second)
}

func (c *GenericConnector) Name() string       { return c.name }
func (c *GenericConnector) Display() string    { return c.display }
func (c *GenericConnector) SupportsPKCE() bool { return true }

func (c *GenericConnector) discover(ctx context.Context) (discoveryDoc, error) {
	c.docMu.Lock()
	defer c.docMu.Unlock()
	if c.docOK && time.Since(c.discoveredAt) < discoveryTTL {
		return c.doc, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.issuerURL+"/.well-known/openid-configuration", nil)
	if err != nil {
		return discoveryDoc{}, fmt.Errorf("oidc/generic: build discovery req: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return discoveryDoc{}, fmt.Errorf("oidc/generic: discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return discoveryDoc{}, fmt.Errorf("oidc/generic: discovery %d", resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOIDCBodyBytes)).Decode(&doc); err != nil {
		return discoveryDoc{}, fmt.Errorf("oidc/generic: decode discovery: %w", err)
	}
	if err := c.validateDiscovery(doc); err != nil {
		return discoveryDoc{}, err
	}
	c.doc = doc
	c.docOK = true
	c.discoveredAt = time.Now()
	return c.doc, nil
}

// validateDiscovery enforces the spec + security checks on a discovery doc:
// required endpoints present, issuer matches the configured issuer (RFC 8414 —
// prevents discovery/issuer confusion), and (strict) discovered endpoints are
// https (defence-in-depth atop the SSRF-pinned transport).
func (c *GenericConnector) validateDiscovery(doc discoveryDoc) error {
	if doc.AuthorizeURL == "" || doc.TokenURL == "" || doc.UserInfoURL == "" {
		return fmt.Errorf("oidc/generic: discovery missing endpoints")
	}
	if doc.Issuer != "" && strings.TrimRight(doc.Issuer, "/") != c.issuerURL {
		return fmt.Errorf("oidc/generic: discovery issuer mismatch")
	}
	if c.strict {
		for _, raw := range []string{doc.AuthorizeURL, doc.TokenURL, doc.UserInfoURL, doc.JWKSURL} {
			if raw == "" {
				continue // jwks_uri is optional today
			}
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" {
				return fmt.Errorf("oidc/generic: discovered endpoint must be https")
			}
		}
	}
	return nil
}

func (c *GenericConnector) AuthorizeURL(ctx context.Context, redirectURI, state, codeVerifier string) (string, error) {
	doc, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(c.scopes, " "))
	q.Set("state", state)
	if codeVerifier != "" {
		q.Set("code_challenge", deriveS256(codeVerifier))
		q.Set("code_challenge_method", "S256")
	}
	sep := "?"
	if strings.Contains(doc.AuthorizeURL, "?") {
		sep = "&"
	}
	return doc.AuthorizeURL + sep + q.Encode(), nil
}

func (c *GenericConnector) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (ExternalUser, error) {
	doc, err := c.discover(ctx)
	if err != nil {
		return ExternalUser{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: build token req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ExternalUser{}, fmt.Errorf("oidc/generic: token endpoint %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOIDCBodyBytes)).Decode(&tok); err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: decode token: %w", err)
	}
	if tok.AccessToken == "" {
		return ExternalUser{}, fmt.Errorf("oidc/generic: empty access token")
	}

	// Verify the ID token signature + iss/aud/exp against the issuer's JWKS —
	// the cryptographic trust anchor that the token response is genuinely from
	// the configured issuer's keys (defence-in-depth atop the https + SSRF +
	// discovery-issuer-match guards). Required in strict mode (org-admin-
	// supplied issuers); best-effort (verify-if-present) otherwise.
	var verifiedSub string
	if tok.IDToken != "" && doc.JWKSURL != "" {
		c.docMu.Lock()
		if c.jwks == nil {
			c.jwks = newJWKSCache(doc.JWKSURL, c.httpClient)
		}
		cache := c.jwks
		c.docMu.Unlock()
		expectedIss := doc.Issuer
		if expectedIss == "" {
			expectedIss = c.issuerURL
		}
		claims, verr := verifyIDToken(ctx, cache, tok.IDToken, expectedIss, c.clientID)
		if verr != nil {
			return ExternalUser{}, verr
		}
		verifiedSub = claims.Subject
	} else if c.strict {
		return ExternalUser{}, fmt.Errorf("oidc/generic: issuer returned no id_token")
	}

	uReq, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.UserInfoURL, nil)
	if err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: build userinfo req: %w", err)
	}
	uReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uReq.Header.Set("Accept", "application/json")
	uResp, err := c.httpClient.Do(uReq)
	if err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: userinfo: %w", err)
	}
	defer uResp.Body.Close()
	if uResp.StatusCode/100 != 2 {
		return ExternalUser{}, fmt.Errorf("oidc/generic: userinfo %d", uResp.StatusCode)
	}
	var info struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.NewDecoder(io.LimitReader(uResp.Body, maxOIDCBodyBytes)).Decode(&info); err != nil {
		return ExternalUser{}, fmt.Errorf("oidc/generic: decode userinfo: %w", err)
	}
	if verifiedSub != "" && info.Sub != verifiedSub {
		// The userinfo subject must match the verified ID-token subject — else
		// the two halves of the response describe different users (token
		// substitution).
		return ExternalUser{}, fmt.Errorf("oidc/generic: userinfo subject mismatch")
	}
	if info.Email == "" {
		return ExternalUser{}, ErrEmailMissing
	}
	if !info.EmailVerified {
		// Reject unverified emails so an adversary who can register the
		// address at the IdP before the owner verifies it cannot claim
		// the iterion account associated with that email.
		return ExternalUser{}, ErrEmailNotVerified
	}
	name := info.Name
	if name == "" {
		name = info.PreferredUsername
	}
	return ExternalUser{
		Provider: c.Name(),
		Subject:  info.Sub,
		Email:    info.Email,
		Name:     name,
	}, nil
}
