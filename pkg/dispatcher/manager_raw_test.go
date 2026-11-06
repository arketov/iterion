package dispatcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawTestManager is an idle manager over a fresh store; projectDir may be "".
func rawTestManager(t *testing.T, projectDir string) (*Manager, string) {
	t.Helper()
	t.Setenv("ITERION_DISPATCHER_AUTOSTART", "0")
	dir := newTestStoreDir(t)
	m, err := NewManager(ManagerOptions{
		StoreDir:    dir,
		NativeStore: newTestNativeStore(t, dir),
		Logger:      newTestLogger(),
		ProjectDir:  projectDir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	return m, filepath.Join(dir, "dispatcher", "dispatcher.json")
}

// The saved dispatcher.json carries `$GITHUB_TOKEN` as a reference and the
// path as written; only the copy the dispatcher runs is expanded.
func TestSaveConfigPersistsRawSecretsAndRelPaths(t *testing.T) {
	t.Setenv("DISPATCH_TEST_GH_TOKEN", "s3cret-value")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "bots", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(project, "bots", "x", "main.bot")
	if err := os.WriteFile(wf, []byte("workflow x: x -> done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, cfgPath := rawTestManager(t, project)

	err := m.SaveConfig(&Config{
		Workflow: "./bots/x/main.bot",
		Tracker: TrackerConfig{Kind: TrackerKindGitHub, GitHub: &GitHubTrackerConfig{
			Repo: "o/r", Token: "$DISPATCH_TEST_GH_TOKEN",
		}},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	disk, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), "s3cret-value") {
		t.Fatalf("the token reached dispatcher.json in clear:\n%s", disk)
	}
	if !strings.Contains(string(disk), "$DISPATCH_TEST_GH_TOKEN") || !strings.Contains(string(disk), "./bots/x/main.bot") {
		t.Fatalf("the saved form must keep the reference and the relative path:\n%s", disk)
	}
	rt := m.Config()
	if rt.Tracker.GitHub.Token != "s3cret-value" {
		t.Fatalf("the running config must have the token expanded, got %q", rt.Tracker.GitHub.Token)
	}
	if rt.Workflow != wf {
		t.Fatalf("a relative workflow is the project's (42b1843e): got %q, want %q", rt.Workflow, wf)
	}
}

// "none" and a negative max_attempts are the operator's explicit opt-outs: they
// survive a save and a reload instead of being turned into the defaults.
func TestSaveConfigKeepsNoneAndNegativeEscapes(t *testing.T) {
	botsDir := t.TempDir()
	m, cfgPath := rawTestManager(t, "")
	err := m.SaveConfig(&Config{
		RequireAssignee: true,
		Tracker:         TrackerConfig{Kind: TrackerKindNative},
		Bots:            botsConfig(botsDir),
		Agent:           AgentConfig{RunningState: "none", MaxAttempts: -1},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	disk, _ := os.ReadFile(cfgPath)
	var onDisk Config
	if err := json.Unmarshal(disk, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Agent.RunningState != "none" || onDisk.Agent.MaxAttempts != -1 {
		t.Fatalf("the escape hatches were rewritten on save: %+v", onDisk.Agent)
	}
	if rt := m.Config(); rt.Agent.RunningState != "" || rt.Agent.MaxAttempts != 0 {
		t.Fatalf("the running config must read them as opt-outs: %+v", rt.Agent)
	}
}

// GET /config masks a literal token but shows an env reference; PUT /config
// reads the mask back as "unchanged", so saving the settings form does not
// overwrite a stored token with "***".
func TestPutConfigMaskedTokenKeepsStoredToken(t *testing.T) {
	m, cfgPath := rawTestManager(t, "")
	botsDir := t.TempDir()
	save := func(token string) {
		t.Helper()
		if err := m.SaveConfig(&Config{
			RequireAssignee: true,
			Bots:            botsConfig(botsDir),
			Tracker: TrackerConfig{Kind: TrackerKindGitLab, GitLab: &GitLabTrackerConfig{
				Host: "https://gitlab.example", Repo: "o/r", Token: token,
			}},
		}); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
	}
	save("glpat-literal")

	rec := httptest.NewRecorder()
	m.handleGetConfig(rec, httptest.NewRequest(http.MethodGet, "/config", nil))
	var got Config
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tracker.GitLab == nil || got.Tracker.GitLab.Token != "***" {
		t.Fatalf("GET /config must mask a literal GitLab token, got %+v", got.Tracker.GitLab)
	}

	// The form sends back what it got.
	body, _ := json.Marshal(got)
	rec = httptest.NewRecorder()
	m.handlePutConfig(rec, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "glpat-literal") {
		t.Fatalf("PUT /config answered with the token in clear: %s", rec.Body.String())
	}
	disk, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(disk), "glpat-literal") || strings.Contains(string(disk), `"***"`) {
		t.Fatalf("the stored token must survive a save of the masked form:\n%s", disk)
	}
	if m.Config().Tracker.GitLab.Token != "glpat-literal" {
		t.Fatalf("the running token changed: %q", m.Config().Tracker.GitLab.Token)
	}
}

func TestRedactedConfigMasksLiteralsAndKeepsEnvReferences(t *testing.T) {
	cfg := &Config{Tracker: TrackerConfig{
		GitHub:  &GitHubTrackerConfig{Token: "ghp_x"},
		Forgejo: &ForgejoTrackerConfig{Token: "$FORGEJO_TOKEN"},
		GitLab:  &GitLabTrackerConfig{Token: "${GITLAB_TOKEN}"},
	}}
	out := redactedConfig(cfg)
	if out.Tracker.GitHub.Token != "***" {
		t.Errorf("github literal token not masked: %q", out.Tracker.GitHub.Token)
	}
	if out.Tracker.Forgejo.Token != "$FORGEJO_TOKEN" || out.Tracker.GitLab.Token != "${GITLAB_TOKEN}" {
		t.Errorf("env references are not secrets and stay visible: %q %q", out.Tracker.Forgejo.Token, out.Tracker.GitLab.Token)
	}
	if cfg.Tracker.GitHub.Token != "ghp_x" {
		t.Error("redactedConfig must not modify its input")
	}
}

// The 409 answer names the way back that exists: ?replace=1 and the button.
func TestApplyDefaults409PointsAtReplace(t *testing.T) {
	m, _ := rawTestManager(t, "")
	m.defaultsFn = func() (*Config, error) { return &Config{}, nil }
	if err := m.SaveConfig(&Config{
		RequireAssignee: true,
		Bots:            botsConfig(t.TempDir()),
		Tracker:         TrackerConfig{Kind: TrackerKindNative},
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.handleApplyDefaults(rec, httptest.NewRequest(http.MethodPost, "/defaults/apply", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	msg := rec.Body.String()
	if !strings.Contains(msg, "replace=1") || !strings.Contains(msg, "Reset to default settings") || strings.Contains(msg, "DELETE") {
		t.Fatalf("the 409 must point at ?replace=1 and the Reset button, and not at DELETE: %s", msg)
	}
}

// A relative path in the saved settings is the project's; with no project known
// it falls back to the folder of dispatcher.json, as before.
func TestManagerResolvesRelativePathsAgainstProjectDir(t *testing.T) {
	project := t.TempDir()
	m, _ := rawTestManager(t, project)
	if err := m.SaveConfig(&Config{
		RequireAssignee: true,
		Tracker:         TrackerConfig{Kind: TrackerKindNative},
		Bots:            botsConfig("./bots"),
		Workspace:       WorkspaceConfig{Root: "ws"},
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	rt := m.Config()
	if len(rt.Bots.Paths) != 1 || rt.Bots.Paths[0] != filepath.Join(project, "bots") {
		t.Fatalf("bots.paths = %v, want it under the project %s", rt.Bots.Paths, project)
	}
	if rt.Workspace.Root != filepath.Join(project, "ws") {
		t.Fatalf("workspace.root = %q, want it under the project", rt.Workspace.Root)
	}
	if raw := m.RawConfig(); raw.Bots.Paths[0] != "./bots" || raw.Workspace.Root != "ws" {
		t.Fatalf("the saved form must keep the relative paths: %+v %+v", raw.Bots, raw.Workspace)
	}
}
