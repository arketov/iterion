package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/store"
)

// TestMissingRunAnswersRunNotFound pins the client text of a run 404: it is
// "run not found" and nothing else. The handlers used to format the store's own
// error into it ("run not found: store: load run <id>: ... open <store>\runs\
// <id>\run.json: ..."), which handed the caller the layout — and on a read
// failure the absolute path — of the user's store.
func TestMissingRunAnswersRunNotFound(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{
		DisableAuth: true,
		WorkDir:     work,
		StoreDir:    filepath.Join(work, ".iterion"),
	}, iterlog.New(iterlog.LevelError, nil))
	srv.handler = srv.mux

	for _, target := range []string{
		"/api/runs/no-such-run",
		"/api/runs/no-such-run/tags",
		"/api/runs/no-such-run/notes",
		"/api/runs/no-such-run/log",
		"/api/runs/no-such-run/plans",
		"/api/runs/no-such-run/route-decisions",
		"/api/runs/no-such-run/commits",
		"/api/runs/no-such-run/files",
		"/api/runs/no-such-run/artifacts",
		// A character Windows refuses in a file name: the read fails with
		// something other than "does not exist" there, and its text names the
		// store's path.
		"/api/runs/a*b",
	} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %q)", rec.Code, rec.Body.String())
			}
			if got := errorBody(t, rec); got != "run not found" {
				t.Errorf("error = %q, want %q", got, "run not found")
			}
		})
	}
}

// TestRunNotFoundKeepsTheCauseInTheLog: the cause moves out of the body, not
// out of the operator's reach — a load failure that is not absence is logged,
// absence (the common case) adds no noise.
func TestRunNotFoundKeepsTheCauseInTheLog(t *testing.T) {
	work := t.TempDir()
	var logs bytes.Buffer
	srv := New(Config{
		DisableAuth: true,
		WorkDir:     work,
		StoreDir:    filepath.Join(work, ".iterion"),
	}, iterlog.New(iterlog.LevelWarn, &logs))

	runJSON := filepath.Join(work, ".iterion", "runs", "r1", "run.json")
	readFailure := fmt.Errorf("store: load run r1: %w", &os.PathError{Op: "open", Path: runJSON, Err: fmt.Errorf("Access is denied.")})

	rec := httptest.NewRecorder()
	srv.runNotFound(rec, httptest.NewRequest(http.MethodGet, "/api/runs/r1", nil), readFailure)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := errorBody(t, rec); got != "run not found" {
		t.Errorf("error = %q, want %q (no run.json, no work dir)", got, "run not found")
	}
	if !strings.Contains(logs.String(), "Access is denied.") {
		t.Errorf("the cause of a read failure is not in the log: %q", logs.String())
	}

	logs.Reset()
	absent := fmt.Errorf("store: load run r1: %w: %w", store.ErrRunNotFound, os.ErrNotExist)
	rec = httptest.NewRecorder()
	srv.runNotFound(rec, httptest.NewRequest(http.MethodGet, "/api/runs/r1", nil), absent)
	if got := errorBody(t, rec); got != "run not found" {
		t.Errorf("absent run: error = %q, want %q", got, "run not found")
	}
	if logs.Len() != 0 {
		t.Errorf("an absent run logged %q, want nothing", logs.String())
	}
}

// TestRunScopeMissingAndInvisibleRunAnswerTheSameBody: ADR-103 — a run the
// caller cannot see answers exactly what a missing run answers, text included.
func TestRunScopeMissingAndInvisibleRunAnswerTheSameBody(t *testing.T) {
	srv, _ := newRunScopeServer(t)
	foreign := caller("u-member-b", "tenant-B")

	body := func(id string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+id, nil)
		if _, ok := srv.scopeRunByID(rec, req, foreign); ok {
			t.Fatalf("%s: served to a caller with no standing", id)
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", id, rec.Code)
		}
		return errorBody(t, rec)
	}

	invisible, missing := body("run-1"), body("no-such-run")
	if invisible != "run not found" || missing != "run not found" {
		t.Errorf("invisible = %q, missing = %q, want both %q", invisible, missing, "run not found")
	}
}
