package tracker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

// fakeForgejo serves a handful of canned responses for the adapter tests.
func newFakeForgejo(t *testing.T) (*httptest.Server, *map[string]int) {
	t.Helper()
	calls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" issues"]++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":     1,
				"title":      "ready one",
				"body":       "body",
				"state":      "open",
				"labels":     []map[string]string{{"name": "ready"}},
				"created_at": "2026-05-01T00:00:00Z",
				"updated_at": "2026-05-01T00:00:00Z",
				"html_url":   "http://forge.example/owner/repo/issues/1",
			},
			{
				"number":     2,
				"title":      "claimed elsewhere",
				"state":      "open",
				"labels":     []map[string]string{{"name": "ready"}, {"name": "iterion-claimed"}},
				"created_at": "2026-05-01T00:00:00Z",
				"updated_at": "2026-05-01T00:00:00Z",
			},
			{
				"number":     3,
				"title":      "unmatched",
				"state":      "open",
				"labels":     []map[string]string{{"name": "junk"}},
				"created_at": "2026-05-01T00:00:00Z",
				"updated_at": "2026-05-01T00:00:00Z",
			},
		})
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" issue1"]++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":     1,
			"title":      "ready one",
			"state":      "open",
			"labels":     []map[string]string{{"name": "ready"}},
			"created_at": "2026-05-01T00:00:00Z",
			"updated_at": "2026-05-01T00:00:00Z",
		})
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" labels1"]++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels/42", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" labels1/42"]++
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/labels", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" labels"]++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 42, "name": "iterion-claimed"},
			})
		case http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": "iterion-claimed"})
		}
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" comments1"]++
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/v1/repos/owner/repo/issues/999/labels", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newForgejo(t *testing.T, host string) *tracker.ForgejoAdapter {
	t.Helper()
	a, err := tracker.NewForgejo(tracker.ForgejoOptions{
		Host:         host,
		Repo:         "owner/repo",
		Token:        "secret",
		ClaimedLabel: "iterion-claimed",
		StateMapping: map[string]tracker.LabelSelector{
			"ready": {LabelsInclude: []string{"ready"}, LabelsExclude: []string{"iterion-claimed"}},
		},
	})
	if err != nil {
		t.Fatalf("NewForgejo: %v", err)
	}
	return a
}

func TestForgejoListCandidates(t *testing.T) {
	srv, _ := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	got, err := a.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0].ID, "#1") {
		t.Fatalf("want 1 candidate (#1), got %+v", got)
	}
	if got[0].WorkflowState != "ready" {
		t.Fatalf("state: %s", got[0].WorkflowState)
	}
}

func TestForgejoRefreshStates(t *testing.T) {
	srv, _ := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	id := fmt.Sprintf("forgejo:%s/owner/repo#1", stripScheme(srv.URL))
	got, err := a.RefreshStates(context.Background(), []string{id, "forgejo:bogus/x#1"})
	if err != nil {
		t.Fatalf("RefreshStates: %v", err)
	}
	if got[id] != "ready" {
		t.Fatalf("want ready, got %q", got[id])
	}
}

func TestForgejoClaimAndRelease(t *testing.T) {
	srv, calls := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	id := fmt.Sprintf("forgejo:%s/owner/repo#1", stripScheme(srv.URL))
	if err := a.Claim(context.Background(), id, "h-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := a.Release(context.Background(), id, "h-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Claim now goes through POST /issues/1/labels (one HTTP call),
	// Release goes through DELETE /issues/1/labels/{id} (one HTTP call).
	// The label-id cache is populated by a single GET /labels.
	if (*calls)["POST labels1"] != 1 {
		t.Fatalf("expected 1 POST labels1 (claim), got %d", (*calls)["POST labels1"])
	}
	if (*calls)["DELETE labels1/42"] != 1 {
		t.Fatalf("expected 1 DELETE labels1/42 (release), got %d", (*calls)["DELETE labels1/42"])
	}
	if (*calls)["GET labels"] != 1 {
		t.Fatalf("expected 1 GET labels (cache fill), got %d", (*calls)["GET labels"])
	}
	// A second claim/release pair must hit the cache — no extra GET labels.
	_ = a.Claim(context.Background(), id, "h-1")
	_ = a.Release(context.Background(), id, "h-1")
	if (*calls)["GET labels"] != 1 {
		t.Fatalf("label cache miss on second pass: %d", (*calls)["GET labels"])
	}
}

func TestForgejoComment(t *testing.T) {
	srv, calls := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	id := fmt.Sprintf("forgejo:%s/owner/repo#1", stripScheme(srv.URL))
	if err := a.Comment(context.Background(), id, "hi"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if (*calls)["POST comments1"] != 1 {
		t.Fatalf("expected 1 comment call, got %d", (*calls)["POST comments1"])
	}
}

func TestForgejoNotFound(t *testing.T) {
	srv, _ := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	id := fmt.Sprintf("forgejo:%s/owner/repo#999", stripScheme(srv.URL))
	if err := a.Claim(context.Background(), id, "h"); !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestForgejoUpdateStateRejected(t *testing.T) {
	srv, _ := newFakeForgejo(t)
	a := newForgejo(t, srv.URL)
	id := fmt.Sprintf("forgejo:%s/owner/repo#1", stripScheme(srv.URL))
	err := a.UpdateState(context.Background(), id, "noplace")
	if !errors.Is(err, tracker.ErrTransitionRejected) {
		t.Fatalf("want ErrTransitionRejected, got %v", err)
	}
}

func TestForgejoTokenHeader(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	a := newForgejo(t, srv.URL)
	_, _ = a.ListCandidates(context.Background())
	if sawAuth != "token secret" {
		t.Fatalf("want 'token secret', got %q", sawAuth)
	}
}

// Compile-time assertion.
var _ tracker.Tracker = (*tracker.ForgejoAdapter)(nil)

// stripScheme removes the http:// prefix and any trailing slash, matching
// the format used in forgejo:<host>/... IDs.
func stripScheme(u string) string {
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	return strings.TrimRight(u, "/")
}
