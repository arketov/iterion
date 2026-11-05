package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/auth/oidc"
	"github.com/arketov/iterion/pkg/auth/orgsso"
	"github.com/arketov/iterion/pkg/identity"
)

// orgServiceFixture wires a Service with an in-memory OrgSSO store plus a team
// and an enabled per-org OIDC provider row. Returns the service, the org id,
// and the provider id.
func orgServiceFixture(t *testing.T, role identity.Role) (*Service, string) {
	t.Helper()
	// Invite-only on purpose: a per-org Keycloak must still onboard new users
	// into its org (the admin opted the IdP in as the org's trust root).
	svc := newTestService(t, SignupInviteOnly)
	store := orgsso.NewMemoryStore()
	svc.orgSSO = store
	ctx := context.Background()
	team, err := svc.store.CreateTeam(ctx, identity.Team{ID: "team-acme", Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := store.Create(ctx, orgsso.OrgSSOProvider{
		ID: "prov1", TenantID: team.ID, Kind: orgsso.KindOIDC, Enabled: true,
		IssuerURL: "https://sso.acme.example/realms/main", ClientID: "iterion",
		DefaultRole: role, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return svc, team.ID
}

func TestLoginForOrg_NewUserOnboardedInviteOnly(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember)
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-1", Email: "bob@acme.example", Name: "Bob"}
	res, err := svc.LoginWithExternalForOrg(context.Background(), ext, teamID, "prov1", "ua", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.ActiveTeamID != teamID {
		t.Errorf("active team = %q, want %q", res.ActiveTeamID, teamID)
	}
	if res.ActiveRole != identity.RoleMember {
		t.Errorf("active role = %q, want member", res.ActiveRole)
	}
	// Idempotent: a second login is a returning user, still lands in the org.
	res2, err := svc.LoginWithExternalForOrg(context.Background(), ext, teamID, "prov1", "ua", "ip")
	if err != nil || res2.ActiveTeamID != teamID {
		t.Fatalf("returning login failed: %v (team %q)", err, res2.ActiveTeamID)
	}
}

func TestLoginForOrg_DefaultRoleAdmin(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleAdmin)
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-2", Email: "ann@acme.example"}
	res, err := svc.LoginWithExternalForOrg(context.Background(), ext, teamID, "prov1", "ua", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.ActiveRole != identity.RoleAdmin {
		t.Errorf("active role = %q, want admin", res.ActiveRole)
	}
}

func TestLoginForOrg_ExistingEmailRequiresConsent(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember)
	ctx := context.Background()
	// Pre-existing password account with the same email.
	if _, _, err := svc.CreateUserAndPersonalTeam(ctx, "carol@acme.example", "Carol", "correcthorse", false, identity.UserStatusActive); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-new", Email: "carol@acme.example"}
	_, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip")
	if !errors.Is(err, ErrLinkRequiresConsent) {
		t.Fatalf("expected ErrLinkRequiresConsent, got %v", err)
	}
}

func TestLoginForOrg_NoDowngrade(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember) // provider grants member
	ctx := context.Background()
	// First login creates the user as member.
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-owner", Email: "dan@acme.example"}
	res, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Manually promote to owner.
	if err := svc.store.UpsertMembership(ctx, identity.Membership{UserID: res.User.ID, TeamID: teamID, Role: identity.RoleOwner, JoinedAt: time.Now()}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// Re-login: the member grant must NOT downgrade the owner.
	res2, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip")
	if err != nil {
		t.Fatalf("relogin: %v", err)
	}
	if res2.ActiveRole != identity.RoleOwner {
		t.Errorf("role downgraded to %q, want owner preserved", res2.ActiveRole)
	}
}

func TestLoginForOrg_WrongTenantRejected(t *testing.T) {
	svc, _ := orgServiceFixture(t, identity.RoleMember)
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-x", Email: "eve@acme.example"}
	// providerID prov1 belongs to team-acme; claiming a different tenant must fail.
	_, err := svc.LoginWithExternalForOrg(context.Background(), ext, "team-other", "prov1", "ua", "ip")
	if !errors.Is(err, oidc.ErrUnknownProvider) {
		t.Fatalf("expected ErrUnknownProvider for tenant mismatch, got %v", err)
	}
}

// enableAutoLink flips AutoLinkOnEmail on the fixture provider and wires a
// domain store, returning it so the caller can verify domains.
func enableAutoLink(t *testing.T, svc *Service) *orgsso.MemoryDomainStore {
	t.Helper()
	ctx := context.Background()
	row, err := svc.orgSSO.Get(ctx, "prov1")
	if err != nil {
		t.Fatal(err)
	}
	row.AutoLinkOnEmail = true
	if err := svc.orgSSO.Update(ctx, row); err != nil {
		t.Fatal(err)
	}
	domains := orgsso.NewMemoryDomainStore()
	svc.domains = domains
	return domains
}

func TestLoginForOrg_AutoLinkVerifiedDomain(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember)
	ctx := context.Background()
	domains := enableAutoLink(t, svc)
	now := time.Now()
	if err := domains.Create(ctx, orgsso.VerifiedDomain{ID: "d1", TenantID: teamID, Domain: "acme.example", VerifiedAt: &now, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.CreateUserAndPersonalTeam(ctx, "bob@acme.example", "Bob", "correcthorse", false, identity.UserStatusActive); err != nil {
		t.Fatal(err)
	}
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-bob", Email: "bob@acme.example"}
	res, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip")
	if err != nil {
		t.Fatalf("auto-link login: %v", err)
	}
	if res.ActiveTeamID != teamID {
		t.Errorf("active team = %q, want %q", res.ActiveTeamID, teamID)
	}
	if _, err := svc.store.GetOIDCLink(ctx, "oidc-org-prov1", "kc-bob"); err != nil {
		t.Errorf("auto-link did not create the OIDC link: %v", err)
	}
}

func TestLoginForOrg_NoAutoLinkUnverifiedDomain(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember)
	ctx := context.Background()
	enableAutoLink(t, svc) // domain store wired but NOTHING verified
	if _, _, err := svc.CreateUserAndPersonalTeam(ctx, "carol@acme.example", "Carol", "correcthorse", false, identity.UserStatusActive); err != nil {
		t.Fatal(err)
	}
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-carol", Email: "carol@acme.example"}
	if _, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip"); !errors.Is(err, ErrLinkRequiresConsent) {
		t.Fatalf("unverified domain must require consent, got %v", err)
	}
}

func TestLoginForOrg_NoAutoLinkSuperAdmin(t *testing.T) {
	svc, teamID := orgServiceFixture(t, identity.RoleMember)
	ctx := context.Background()
	domains := enableAutoLink(t, svc)
	now := time.Now()
	_ = domains.Create(ctx, orgsso.VerifiedDomain{ID: "d1", TenantID: teamID, Domain: "acme.example", VerifiedAt: &now, CreatedAt: now})
	// Verified domain + opt-in, but the target is a super-admin → never auto-link.
	if _, _, err := svc.CreateUserAndPersonalTeam(ctx, "root@acme.example", "Root", "correcthorse", true, identity.UserStatusActive); err != nil {
		t.Fatal(err)
	}
	ext := oidc.ExternalUser{Provider: "oidc-org-prov1", Subject: "kc-root", Email: "root@acme.example"}
	if _, err := svc.LoginWithExternalForOrg(ctx, ext, teamID, "prov1", "ua", "ip"); !errors.Is(err, ErrLinkRequiresConsent) {
		t.Fatalf("super-admin auto-link must be refused, got %v", err)
	}
}

func TestLoginForOrg_NilStoreDisabled(t *testing.T) {
	svc := newTestService(t, SignupOpen) // no orgSSO wired
	ext := oidc.ExternalUser{Provider: "oidc-org-x", Subject: "s", Email: "a@b.c"}
	_, err := svc.LoginWithExternalForOrg(context.Background(), ext, "t", "x", "ua", "ip")
	if !errors.Is(err, oidc.ErrUnknownProvider) {
		t.Fatalf("expected ErrUnknownProvider when orgSSO nil, got %v", err)
	}
}
