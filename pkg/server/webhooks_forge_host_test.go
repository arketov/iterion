package server

import (
	"testing"

	"github.com/arketov/iterion/pkg/webhooks"
)

const mrURL = "https://gitlab.example.com/group/proj/-/merge_requests/5"

func TestResolveForgeBaseURL_Pin(t *testing.T) {
	t.Setenv("ITERION_WEBHOOK_FORGE_HOSTS", "") // no global allowlist

	// No pin, no allowlist → derive the host from the payload.
	if base, refusal := resolveForgeBaseURL(webhooks.Config{}, mrURL); refusal != "" || base != "https://gitlab.example.com" {
		t.Errorf("no-pin: base=%q refusal=%q, want https://gitlab.example.com / no refusal", base, refusal)
	}

	// Pinned + matching host → use the pin.
	cfg := webhooks.Config{ForgeBaseURL: "https://gitlab.example.com"}
	if base, refusal := resolveForgeBaseURL(cfg, mrURL); refusal != "" || base != "https://gitlab.example.com" {
		t.Errorf("pin-match: base=%q refusal=%q, want match", base, refusal)
	}

	// Pinned + mismatching payload host → refuse (the token-exfil case).
	evil := "https://evil.example.net/x/y/-/merge_requests/1"
	if base, refusal := resolveForgeBaseURL(cfg, evil); refusal == "" {
		t.Errorf("pin-mismatch: want refusal, got base=%q", base)
	}

	// Host comparison is case-insensitive (RFC 3986).
	if _, refusal := resolveForgeBaseURL(webhooks.Config{ForgeBaseURL: "https://GitLab.Example.com"}, mrURL); refusal != "" {
		t.Errorf("case-insensitive: unexpected refusal %q", refusal)
	}

	// Unparseable payload URL → refuse.
	if _, refusal := resolveForgeBaseURL(webhooks.Config{}, "://not a url"); refusal == "" {
		t.Error("bad payload host: want refusal")
	}
}

func TestResolveForgeBaseURL_GlobalAllowlist(t *testing.T) {
	t.Setenv("ITERION_WEBHOOK_FORGE_HOSTS", "gitlab.example.com")
	if _, refusal := resolveForgeBaseURL(webhooks.Config{}, mrURL); refusal != "" {
		t.Errorf("allowlisted host: unexpected refusal %q", refusal)
	}
	other := "https://other.example.org/a/b/-/merge_requests/1"
	if _, refusal := resolveForgeBaseURL(webhooks.Config{}, other); refusal == "" {
		t.Error("non-allowlisted host: want refusal")
	}
}

func TestValidateForgeBaseURL(t *testing.T) {
	cases := []struct {
		in  string
		out string
		ok  bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"https://gitlab.example.com", "https://gitlab.example.com", true},
		{"https://gitlab.example.com/", "https://gitlab.example.com", true},
		{"https://gitlab.example.com/group/proj", "https://gitlab.example.com", true},
		{"http://gitlab.example.com", "", false},            // not https
		{"gitlab.example.com", "", false},                   // no scheme
		{"https://user:pass@gitlab.example.com", "", false}, // userinfo
		{"https://", "", false},                             // no host
	}
	for _, c := range cases {
		got, err := validateForgeBaseURL(c.in)
		if (err == nil) != c.ok {
			t.Errorf("validateForgeBaseURL(%q) ok=%v, want %v (err=%v)", c.in, err == nil, c.ok, err)
			continue
		}
		if c.ok && got != c.out {
			t.Errorf("validateForgeBaseURL(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
