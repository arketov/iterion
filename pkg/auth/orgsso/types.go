// Package orgsso owns the per-tenant (per-org) SSO provider configuration:
// the rows an iterion org admin self-serves to enable login via their own
// Keycloak (a discovery-based OIDC provider) or to gate the deployment-level
// GitHub login on specific GitHub teams.
//
// It mirrors the per-tenant forge OAuth-app store (pkg/forge/oauth_app_store.go):
// one unified collection, rows discriminated by Kind, client secrets sealed at
// rest via secrets.Sealer with AAD "org_sso_provider:<id>". Get is keyed by id
// only — the HTTP layer asserts tenant ownership before mutating.
//
// Security posture (see docs/cloud-admin.md): an org-admin-supplied issuer URL
// drives server-side discovery/token/userinfo fetches, so every outbound call
// is routed through the shared SSRF guard (pkg/secure/httpdial). SSO grant
// roles can never be `owner` (a static invariant enforced in Validate) and the
// dynamic ceiling (≤ the configuring admin's role) is enforced by the route
// handler.
package orgsso

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/arketov/iterion/pkg/identity"
)

// Kind discriminates the two provider shapes stored in the unified collection.
type Kind string

const (
	// KindOIDC is a per-org discovery-based OIDC provider (Keycloak, Auth0,
	// Azure AD, …). Carries issuer URL + client credentials.
	KindOIDC Kind = "oidc"
	// KindGitHub gates the deployment-level GitHub login on allow-listed
	// GitHub teams. Carries no credentials of its own (it reuses the global
	// GitHub OAuth app); only the grant list + auto-provision policy.
	KindGitHub Kind = "github"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindOIDC || k == KindGitHub }

// oidcSlugPrefix namespaces the per-org OIDC connector slug so the HTTP
// dispatcher can tell a per-org provider (resolved from this store) apart from
// a global connector ("github", "google", legacy "sso") in the static
// registry. The slug embeds the provider ID (a UUID) so it is stable across an
// org rename and unique across orgs — the value an admin registers as the
// redirect URI at their IdP.
const oidcSlugPrefix = "oidc-org-"

// OIDCSlug returns the URL slug used for this provider's /api/auth/oidc/<slug>/…
// routes. Only meaningful for KindOIDC rows.
func (p OrgSSOProvider) OIDCSlug() string { return oidcSlugPrefix + p.ID }

// ParseOIDCSlug extracts the provider ID from a per-org OIDC slug. ok is false
// for global connector slugs (which the caller resolves from the static
// registry instead).
func ParseOIDCSlug(slug string) (id string, ok bool) {
	return strings.CutPrefix(slug, oidcSlugPrefix)
}

// OrgSSOProvider is one per-tenant SSO configuration row.
type OrgSSOProvider struct {
	ID          string `bson:"_id" json:"id"`
	TenantID    string `bson:"tenant_id" json:"tenant_id"`
	Kind        Kind   `bson:"kind" json:"kind"`
	Enabled     bool   `bson:"enabled" json:"enabled"`
	DisplayName string `bson:"display_name,omitempty" json:"display_name,omitempty"`

	// ---- KindOIDC fields ----

	// IssuerURL is the OIDC issuer (no trailing slash); discovery hits
	// <IssuerURL>/.well-known/openid-configuration. Stored in the clear.
	IssuerURL string `bson:"issuer_url,omitempty" json:"issuer_url,omitempty"`
	// ClientID is stored in the clear (the admin UI lists it). SealedSecret
	// holds the client_secret sealed via secrets.Sealer with AAD
	// "org_sso_provider:<ID>" — never serialised out of the server.
	ClientID     string   `bson:"client_id,omitempty" json:"client_id,omitempty"`
	SealedSecret []byte   `bson:"sealed_secret,omitempty" json:"-"`
	Scopes       []string `bson:"scopes,omitempty" json:"scopes,omitempty"`
	// DefaultRole is the membership role granted to a user who logs in via
	// this OIDC provider for this org. Defaults to member; never owner.
	DefaultRole identity.Role `bson:"default_role,omitempty" json:"default_role,omitempty"`
	// AutoLinkOnEmail is a Phase-3 opt-in (default off): auto-link a fresh
	// OIDC identity onto an existing iterion user matched by verified email.
	// Stored now so the shape is stable; NOT acted upon until JWKS ID-token
	// verification lands (the safe-auto-link prerequisite).
	AutoLinkOnEmail bool `bson:"auto_link_on_email,omitempty" json:"auto_link_on_email,omitempty"`

	// ---- KindGitHub fields ----

	// Grants maps (GitHub org, team) → iterion role. Evaluated at GitHub
	// login: a user whose GitHub teams intersect Grants is granted membership
	// in this org (Phase 2). Ordered: the first matching grant wins.
	Grants []GitHubTeamGrant `bson:"grants,omitempty" json:"grants,omitempty"`
	// GitHubTeamKeys is the materialised reverse-lookup view of Grants
	// ("<org>/<team_slug>" + "<org>/*", lowercased), maintained on write so a
	// GitHub login resolves matching orgs with one $in query instead of a
	// cross-tenant scan. Internal — never serialised.
	GitHubTeamKeys []string `bson:"github_team_keys,omitempty" json:"-"`
	// AutoProvision: when true a matching user is auto-added to this org; when
	// false, login is allowed only if a membership already exists (the admin
	// must invite first).
	AutoProvision bool `bson:"auto_provision,omitempty" json:"auto_provision"`

	CreatedBy string    `bson:"created_by" json:"created_by"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// GitHubTeamGrant maps a (GitHub org, team) to an iterion role. TeamSlug "" or
// "*" matches any team in (i.e. plain membership of) the GitHub org. Numeric
// IDs are captured for stable matching across org/team renames; the login
// path matches on IDs when present, falling back to lowercased login/slug.
type GitHubTeamGrant struct {
	GitHubOrg   string        `bson:"github_org" json:"github_org"`
	GitHubOrgID int64         `bson:"github_org_id,omitempty" json:"github_org_id,omitempty"`
	TeamSlug    string        `bson:"team_slug,omitempty" json:"team_slug,omitempty"`
	TeamID      int64         `bson:"team_id,omitempty" json:"team_id,omitempty"`
	Role        identity.Role `bson:"role" json:"role"`
	// Verified is set by the route handler once the org has proven control of
	// GitHubOrg (Phase 2: a verified forge connection). An unverified grant is
	// stored but inert at login time — surfaced as "pending verification".
	Verified bool `bson:"verified,omitempty" json:"verified"`
}

// Sentinel errors. The HTTP layer maps these to status codes.
var (
	ErrNotFound      = errors.New("orgsso: provider not found")
	ErrExists        = errors.New("orgsso: a github provider already exists for this org")
	ErrInvalid       = errors.New("orgsso: invalid provider definition")
	ErrOwnerNotGrant = errors.New("orgsso: owner cannot be granted via SSO")
)

// teamKey builds the lowercased reverse-lookup key for one (org, team) pair.
func teamKey(org, team string) string {
	org = strings.ToLower(strings.TrimSpace(org))
	team = strings.ToLower(strings.TrimSpace(team))
	if team == "" || team == "*" {
		return org + "/*"
	}
	return org + "/" + team
}

// FlattenGitHubTeamKeys derives the materialised GitHubTeamKeys from a grant
// list (lowercased, deduped). Wildcard grants collapse to "<org>/*". Only
// VERIFIED grants are included — an unverified grant (the team hasn't proven it
// controls that GitHub org) is stored but inert at login.
func FlattenGitHubTeamKeys(grants []GitHubTeamGrant) []string {
	seen := make(map[string]struct{}, len(grants))
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		if !g.Verified {
			continue
		}
		k := teamKey(g.GitHubOrg, g.TeamSlug)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// capGitHubRole clamps a GitHub-grant role to at most member (see Validate).
func capGitHubRole(r identity.Role) identity.Role {
	if !r.Valid() || r.AtLeast(identity.RoleAdmin) {
		return identity.RoleMember
	}
	return r
}

// RoleForGroups returns the role this GitHub row grants a user holding the
// given (lowercased) group keys — the first matching grant in declaration
// order, clamped to at most member. ok is false when no grant matches.
func (p OrgSSOProvider) RoleForGroups(groups []string) (identity.Role, bool) {
	if p.Kind != KindGitHub {
		return "", false
	}
	set := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		set[strings.ToLower(g)] = struct{}{}
	}
	for _, g := range p.Grants {
		if !g.Verified {
			continue
		}
		if _, ok := set[teamKey(g.GitHubOrg, g.TeamSlug)]; ok {
			return capGitHubRole(g.Role), true
		}
	}
	return "", false
}

// Normalize canonicalises a row in place before persistence: trims the issuer
// URL, defaults scopes/role, and (re)materialises GitHubTeamKeys.
func (p *OrgSSOProvider) Normalize() {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	switch p.Kind {
	case KindOIDC:
		p.IssuerURL = strings.TrimRight(strings.TrimSpace(p.IssuerURL), "/")
		p.ClientID = strings.TrimSpace(p.ClientID)
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"openid", "email", "profile"}
		}
		if p.DefaultRole == "" {
			p.DefaultRole = identity.RoleMember
		}
		// OIDC rows carry no GitHub state.
		p.Grants = nil
		p.GitHubTeamKeys = nil
	case KindGitHub:
		for i := range p.Grants {
			p.Grants[i].GitHubOrg = strings.ToLower(strings.TrimSpace(p.Grants[i].GitHubOrg))
			p.Grants[i].TeamSlug = strings.ToLower(strings.TrimSpace(p.Grants[i].TeamSlug))
			if p.Grants[i].Role == "" {
				p.Grants[i].Role = identity.RoleMember
			}
		}
		p.GitHubTeamKeys = FlattenGitHubTeamKeys(p.Grants)
		// GitHub rows carry no OIDC credentials.
		p.IssuerURL = ""
		p.ClientID = ""
		p.SealedSecret = nil
		p.Scopes = nil
	}
}

// Validate enforces the kind-specific contract + the static SSO security
// invariants (https issuer, no owner grant, valid roles). Call after Normalize.
func (p *OrgSSOProvider) Validate() error {
	if p.ID == "" || p.TenantID == "" {
		return fmt.Errorf("%w: id and tenant_id required", ErrInvalid)
	}
	if !p.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, p.Kind)
	}
	switch p.Kind {
	case KindOIDC:
		if p.IssuerURL == "" {
			return fmt.Errorf("%w: issuer_url required for oidc", ErrInvalid)
		}
		u, err := url.Parse(p.IssuerURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("%w: invalid issuer_url", ErrInvalid)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("%w: issuer_url must be https", ErrInvalid)
		}
		if p.ClientID == "" {
			return fmt.Errorf("%w: client_id required for oidc", ErrInvalid)
		}
		if !p.DefaultRole.Valid() {
			return fmt.Errorf("%w: invalid default_role %q", ErrInvalid, p.DefaultRole)
		}
		if p.DefaultRole == identity.RoleOwner {
			return ErrOwnerNotGrant
		}
	case KindGitHub:
		if len(p.Grants) == 0 {
			return fmt.Errorf("%w: github requires at least one grant", ErrInvalid)
		}
		for _, g := range p.Grants {
			if g.GitHubOrg == "" {
				return fmt.Errorf("%w: grant missing github_org", ErrInvalid)
			}
			if !g.Role.Valid() {
				return fmt.Errorf("%w: invalid grant role %q", ErrInvalid, g.Role)
			}
			// GitHub grants are the H-8 abuse surface (an admin could
			// allow-list a GitHub org they don't control); cap them at member
			// — no admin/owner via a GitHub team allow-list. Proof-of-control +
			// higher roles are a tracked follow-up.
			if g.Role.AtLeast(identity.RoleAdmin) {
				return fmt.Errorf("%w: github grant role is capped at member (got %q)", ErrInvalid, g.Role)
			}
		}
	}
	return nil
}
