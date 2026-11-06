package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const openScopeSecret = "API_TOKEN=hunter2-do-not-serve"

// postOpen posts one path to /api/files/open and returns the raw answer.
func postOpen(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(openFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleOpenFile(rec, httptest.NewRequest(http.MethodPost, "/api/files/open", bytes.NewReader(body)))
	return rec
}

func openScopeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ErrorCode
}

// TestOpenFile_ServesWorkflowFilesOnly is the witness of the card: the route
// answers the editor, but it answers with the file's bytes even for a file that
// does not parse, so `{"path":".env"}` read the project's secrets back whole.
// The name is judged first and the answer is the same whether or not the file
// exists, so the route is no probe for what the project holds either.
func TestOpenFile_ServesWorkflowFilesOnly(t *testing.T) {
	workdir := t.TempDir()
	for name, content := range map[string]string{
		".env":                openScopeSecret,
		"config/secrets.yaml": openScopeSecret,
		"notes.txt":           openScopeSecret,
		"draft.bot.yaml":      openScopeSecret, // an author document is no workflow file
		"pack.botz":           openScopeSecret, // nor is a bundle archive
		"ok.bot":              "workflow ok:\n  entry: done\n",
	} {
		full := filepath.Join(workdir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{cfg: Config{WorkDir: workdir}}

	for _, path := range []string{".env", "config/secrets.yaml", "notes.txt", "draft.bot.yaml", "pack.botz", "missing.env", "../outside.txt"} {
		rec := postOpen(t, s, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("open %q: status %d, want 400; body %s", path, rec.Code, rec.Body.String())
		}
		if code := openScopeErrorCode(t, rec); code != "not_workflow_file" {
			t.Errorf("open %q: error_code %q, want not_workflow_file; body %s", path, code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("open %q served the file's content: %s", path, rec.Body.String())
		}
	}

	// A workflow still opens, and so does a missing one (the usual 404 — only
	// the name decides the refusal above).
	if rec := postOpen(t, s, "ok.bot"); rec.Code != http.StatusOK {
		t.Errorf("open ok.bot: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := postOpen(t, s, "absent.bot"); rec.Code != http.StatusNotFound {
		t.Errorf("open absent.bot: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

// TestOpenFile_ASymlinkNamedLikeAWorkflowDoesNotOpenAConfigFile: safePath
// follows links inside the workspace, so `link.bot` -> `.env` is a workflow by
// name only; the file it resolves to is judged as well.
func TestOpenFile_ASymlinkNamedLikeAWorkflowDoesNotOpenAConfigFile(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte(openScopeSecret), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env", filepath.Join(workdir, "link.bot")); err != nil {
		t.Skipf("no symlink on this host: %v", err)
	}
	s := &Server{cfg: Config{WorkDir: workdir}}

	rec := postOpen(t, s, "link.bot")
	if rec.Code != http.StatusBadRequest || openScopeErrorCode(t, rec) != "not_workflow_file" {
		t.Errorf("open link.bot: status %d, body %s; want 400 not_workflow_file", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("open link.bot served the linked file's content: %s", rec.Body.String())
	}
}
