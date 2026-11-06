package dispatcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	workflow := filepath.Join(dir, "wf.bot")
	if err := os.WriteFile(workflow, []byte("workflow x:\n  done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body = strings.ReplaceAll(body, "{{WORKFLOW}}", "wf.bot")
	cfgPath := filepath.Join(dir, "iterion.dispatcher.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestConfigLoadNative(t *testing.T) {
	p := writeConfig(t, `name: smoke
workflow: {{WORKFLOW}}
tracker:
  kind: native
polling:
  interval_ms: 12345
agent:
  max_concurrent: 3
dispatch:
  vars:
    user_prompt: "Issue {{ issue.identifier }}: {{ issue.title }}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "smoke" {
		t.Fatalf("name: %q", cfg.Name)
	}
	if cfg.Tracker.Kind != "native" {
		t.Fatalf("kind: %q", cfg.Tracker.Kind)
	}
	if cfg.Polling.IntervalMS != 12345 {
		t.Fatalf("polling interval: %d", cfg.Polling.IntervalMS)
	}
	if cfg.Agent.MaxConcurrent != 3 {
		t.Fatalf("max concurrent: %d", cfg.Agent.MaxConcurrent)
	}
	if !filepath.IsAbs(cfg.Workflow) {
		t.Fatalf("workflow path should be absolute: %s", cfg.Workflow)
	}
}

func TestConfigDefaults(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Polling.IntervalMS != DefaultPollingInterval {
		t.Fatalf("default polling: %d", cfg.Polling.IntervalMS)
	}
	if cfg.Agent.MaxConcurrent != DefaultMaxConcurrent {
		t.Fatalf("default max concurrent: %d", cfg.Agent.MaxConcurrent)
	}
	if cfg.Stall.TimeoutMS == nil || *cfg.Stall.TimeoutMS != DefaultStallTimeoutMS {
		t.Fatalf("default stall: %v", cfg.Stall.TimeoutMS)
	}
	if cfg.Agent.CompletedState != DefaultCompletedState {
		t.Fatalf("default completed_state: %q, want %q", cfg.Agent.CompletedState, DefaultCompletedState)
	}
	// A studio-merged dispatcher run must auto-close its ticket by
	// default — without this, merged issues pile up in "review" forever.
	if cfg.Agent.MergedState != DefaultMergedState {
		t.Fatalf("default merged_state: %q, want %q", cfg.Agent.MergedState, DefaultMergedState)
	}
}

func TestConfigMergedStateNoneOptOut(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
agent:
  merged_state: none
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.MergedState != "" {
		t.Fatalf("merged_state: none should disable (empty), got %q", cfg.Agent.MergedState)
	}
}

func TestConfigMissingWorkflow(t *testing.T) {
	p := writeConfig(t, `workflow: ./nope.bot
tracker:
  kind: native
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Fatalf("expected workflow-not-found error, got %v", err)
	}
}

func TestConfigUnknownTracker(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: weirdo
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "weirdo") {
		t.Fatalf("expected unsupported tracker error, got %v", err)
	}
}

func TestConfigGitHubValidation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: github
  github:
    repo: SocialGouv/iterion
    token: $GITHUB_TOKEN
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker.GitHub == nil || cfg.Tracker.GitHub.Token != "ghp_secret" {
		t.Fatalf("env not expanded: %+v", cfg.Tracker.GitHub)
	}
	if cfg.Tracker.GitHub.ClaimedLabel != DefaultGitHubClaimedLabel {
		t.Fatalf("default claimed_label not applied: %q", cfg.Tracker.GitHub.ClaimedLabel)
	}
}

func TestConfigGitHubBadRepo(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: github
  github:
    repo: bare
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "owner/repo") {
		t.Fatalf("expected owner/repo error, got %v", err)
	}
}

func TestConfigForgejoValidation(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: forgejo
  forgejo:
    host: https://codeberg.org
    repo: forgejo/forgejo
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tracker.Forgejo.Host == "" {
		t.Fatal("host empty after load")
	}
}

func TestConfigTemplateValidation(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
dispatch:
  vars:
    bad: "hello {{issue.notreal}}"
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "notreal") {
		t.Fatalf("expected template field error, got %v", err)
	}
}

func TestConfigHooksValidation(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
hooks:
  after_create:
    script: "echo ok"
    path: "/bin/true"
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "script/path") {
		t.Fatalf("expected hook validation error, got %v", err)
	}
}

func TestConfigWorkspacePersistEnum(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
workspace:
  persist: forever
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "persist") {
		t.Fatalf("expected persist enum error, got %v", err)
	}
}

func TestConfigHomeExpansion(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
workspace:
  root: "~/iterion-ws"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasPrefix(cfg.Workspace.Root, "/tmp/fakehome/") {
		t.Fatalf("home not expanded: %s", cfg.Workspace.Root)
	}
}

// writeConfigWithAssigneeWorkflow extends writeConfig by also creating
// a sibling .bot for the named assignee so the validator's stat check
// on assignee_workflows entries passes.
func writeConfigWithAssigneeWorkflow(t *testing.T, body string) string {
	t.Helper()
	cfgPath := writeConfig(t, body)
	dir := filepath.Dir(cfgPath)
	if err := os.WriteFile(filepath.Join(dir, "bot.bot"), []byte("workflow x:\n  done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestConfigAssigneeDispatch(t *testing.T) {
	p := writeConfigWithAssigneeWorkflow(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
assignee_workflows:
  feature-bot: ./bot.bot
assignee_dispatch:
  feature-bot:
    vars:
      feature_prompt: "Issue {{ issue.identifier }}: {{ issue.title }}\n\n{{ issue.body }}"
      workspace_dir: "{{ dispatcher.workspace_path }}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, ok := cfg.AssigneeDispatch["feature-bot"]
	if !ok {
		t.Fatalf("assignee_dispatch[feature-bot] missing: %+v", cfg.AssigneeDispatch)
	}
	if entry.Vars["feature_prompt"] == "" || entry.Vars["workspace_dir"] == "" {
		t.Fatalf("vars: %+v", entry.Vars)
	}
}

func TestConfigAssigneeDispatchOrphan(t *testing.T) {
	// assignee_dispatch[bot] without a matching assignee_workflows entry
	// is rejected: the override would never fire and indicates a typo.
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
assignee_dispatch:
  ghost-bot:
    vars:
      x: "{{ issue.title }}"
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "ghost-bot") {
		t.Fatalf("expected orphan error, got %v", err)
	}
}

func TestConfigAssigneeDispatchBadTemplate(t *testing.T) {
	p := writeConfigWithAssigneeWorkflow(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
assignee_workflows:
  feature-bot: ./bot.bot
assignee_dispatch:
  feature-bot:
    vars:
      feature_prompt: "{{ issue.notreal }}"
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "notreal") {
		t.Fatalf("expected template field error, got %v", err)
	}
}

// TestConfigDispatchAttachmentsRejected pins that dispatch.attachments is
// a load-time error, not a silent no-op. The dispatcher renders no
// attachments and the engine has no path to inject per-issue attachments
// (they are binary files, not template-rendered strings); the prior
// behaviour parsed + validated the block then dropped it on the floor,
// losing context with zero signal. See ADR-013.
func TestConfigDispatchAttachmentsRejected(t *testing.T) {
	p := writeConfig(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
dispatch:
  attachments:
    context_doc: "{{ issue.body }}"
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected dispatch.attachments to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "dispatch.attachments") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected an explicit 'dispatch.attachments ... not supported' error, got %v", err)
	}
}

// TestConfigAssigneeDispatchAttachmentsRejected pins the same rejection on
// the per-assignee override path (assignee_dispatch[].attachments).
func TestConfigAssigneeDispatchAttachmentsRejected(t *testing.T) {
	p := writeConfigWithAssigneeWorkflow(t, `workflow: {{WORKFLOW}}
tracker:
  kind: native
assignee_workflows:
  feature-bot: ./bot.bot
assignee_dispatch:
  feature-bot:
    attachments:
      spec: "{{ issue.body }}"
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected assignee_dispatch[].attachments to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "feature-bot") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected an explicit per-assignee attachments rejection, got %v", err)
	}
}

// An explicit stall.timeout_ms: 0 is the operator's "off"; an absent key is the
// default; a negative value is the legacy synonym of 0.
func TestStallTimeoutZeroDisables(t *testing.T) {
	const head = "workflow: {{WORKFLOW}}\ntracker:\n  kind: native\n"
	cases := []struct {
		name    string
		extra   string
		want    time.Duration
		wantMS  int
		wantRaw *int // nil = LoadRaw must keep the key unset
	}{
		{"omitted is the default", "", 10 * time.Minute, DefaultStallTimeoutMS, nil},
		{"zero disables", "stall:\n  timeout_ms: 0\n", 0, 0, new(0)},
		{"negative disables", "stall:\n  timeout_ms: -1\n", 0, 0, new(-1)},
		{"explicit value", "stall:\n  timeout_ms: 30000\n", 30 * time.Second, 30000, new(30000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, head+tc.extra)
			cfg, err := Load(p)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Stall.TimeoutMS == nil || *cfg.Stall.TimeoutMS != tc.wantMS {
				t.Fatalf("Load: Stall.TimeoutMS = %v, want %d", cfg.Stall.TimeoutMS, tc.wantMS)
			}
			if got := cfg.StallTimeout(); got != tc.want {
				t.Fatalf("Load: StallTimeout() = %v, want %v", got, tc.want)
			}

			raw, err := LoadRaw(p)
			if err != nil {
				t.Fatalf("LoadRaw: %v", err)
			}
			switch {
			case tc.wantRaw == nil && raw.Stall.TimeoutMS != nil:
				t.Fatalf("LoadRaw must keep an absent key unset, got %d", *raw.Stall.TimeoutMS)
			case tc.wantRaw != nil && (raw.Stall.TimeoutMS == nil || *raw.Stall.TimeoutMS != *tc.wantRaw):
				t.Fatalf("LoadRaw: Stall.TimeoutMS = %v, want %d", raw.Stall.TimeoutMS, *tc.wantRaw)
			}
			res, err := raw.Resolved(filepath.Dir(p), nil)
			if err != nil {
				t.Fatalf("Resolved: %v", err)
			}
			if got := res.StallTimeout(); got != tc.want {
				t.Fatalf("Resolved: StallTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}
