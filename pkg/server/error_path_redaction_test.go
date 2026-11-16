package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/runtime"
)

func namesHostPath(s, work string) bool {
	return strings.Contains(s, work) || strings.Contains(s, filepath.ToSlash(work))
}

// The launch-denial envelope writes its 4xx arm without WriteErrorJSON, and
// "detail" is built from the error of the operation that failed.
func TestLaunchDenialDetailNamesNoHostPath(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{DisableAuth: true, WorkDir: work}, iterlog.New(iterlog.LevelError, nil))
	runJSON := filepath.Join(work, ".iterion", "runs", "r1", "run.json")
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusServiceUnavailable} {
		rec := httptest.NewRecorder()
		srv.renderLaunchDenial(rec, httptest.NewRequest(http.MethodPost, "/x", nil), &launchDenial{status: status, reason: "fork_run_unfunded", detail: "open " + runJSON + ": denied"})
		if namesHostPath(rec.Body.String(), work) {
			t.Errorf("%d body names the workspace: %s", status, rec.Body.String())
		}
		if status != http.StatusUnprocessableEntity {
			continue
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		if body["error"] != "fork_run_unfunded" || !strings.Contains(body["detail"], "run.json") {
			t.Errorf("body = %v, want the reason kept and the file name in detail", body)
		}
	}
}

// The remedy a RuntimeError carries rides in "hint" beside the redacted "error".
func TestResumeErrorHintNamesNoHostPath(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{DisableAuth: true, WorkDir: work}, iterlog.New(iterlog.LevelError, nil))
	runJSON := filepath.Join(work, ".iterion", "runs", "r1", "run.json")
	rec := httptest.NewRecorder()
	srv.writeResumeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), &runtime.RuntimeError{
		Code: runtime.ErrCodeScratchNotPortable, Message: "cannot resume", Hint: "check " + runJSON,
	})
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	hint, _ := body["hint"].(string)
	if hint == "" || namesHostPath(hint, work) {
		t.Errorf("hint = %q, want a hint without the workspace", hint)
	}
}

// The run socket's error envelope and its close frame carry error text too.
func TestRunWSErrorAndCloseFrameNameNoHostPath(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{DisableAuth: true, WorkDir: work}, iterlog.New(iterlog.LevelError, nil))
	runJSON := filepath.Join(work, ".iterion", "runs", "r1", "run.json")

	t.Run("error envelope", func(t *testing.T) {
		rc := newRunConn(srv, nil, "r1")
		rc.sendError("snapshot_failed", "open "+runJSON+": denied", "a1")
		var env runWSEnvelope
		if err := json.Unmarshal(<-rc.sendCh, &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		var p wsErrorPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Code != "snapshot_failed" || namesHostPath(p.Message, work) || !strings.Contains(p.Message, "run.json") {
			t.Errorf("payload = %+v, want the code kept and no workspace in the message", p)
		}
	})

	t.Run("close frame", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = writeWSClose(conn, websocket.ClosePolicyViolation, wsCloseCredentialRefused, "open "+runJSON+": denied")
		}))
		t.Cleanup(ts.Close)
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		ce, _ := awaitWSClose(t, conn, 10*time.Second)
		if ce == nil || namesHostPath(ce.Text, work) || !strings.HasPrefix(ce.Text, wsCloseCredentialRefused) {
			t.Errorf("close = %+v, want the token first and no workspace", ce)
		}
	})
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out.Error
}

// TestClientErrorsDoNotNameTheWorkspaceOrStore is the witness of the card: a
// handler that formats the error of a file operation into a 4xx
// ("load diagnostic: %v" of a store that cannot open run.json) handed the
// caller the absolute directory of the store. Every httpError / httpErrorFor /
// httpErrorCode answer now goes through the redactor the server installs, so
// the message keeps what is relative to the workspace and loses the rest.
func TestClientErrorsDoNotNameTheWorkspaceOrStore(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{DisableAuth: true, WorkDir: work}, iterlog.New(iterlog.LevelError, nil))
	runJSON := filepath.Join(work, ".iterion", "runs", "r1", "run.json")

	t.Run("httpErrorFor", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.httpErrorFor(rec, httptest.NewRequest(http.MethodGet, "/x", nil), http.StatusNotFound, "load diagnostic: open %s: no such file or directory", runJSON)
		got := errorBody(t, rec)
		if strings.Contains(got, work) || strings.Contains(got, filepath.ToSlash(work)) {
			t.Errorf("the 404 names the workspace: %q", got)
		}
		if !strings.Contains(got, "run.json") || !strings.Contains(got, "no such file or directory") {
			t.Errorf("the 404 lost what a reader needs: %q", got)
		}
	})

	t.Run("httpError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpError(rec, http.StatusBadRequest, "invalid path: %s escapes", filepath.ToSlash(runJSON))
		if got := errorBody(t, rec); strings.Contains(got, filepath.ToSlash(work)) {
			t.Errorf("the 400 names the workspace: %q", got)
		}
	})

	t.Run("a Windows spelling of a path the server never saw", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpError(rec, http.StatusNotFound, `stat C:\Users\zz-nobody\store\runs\r9\run.json: The system cannot find the file specified.`)
		got := errorBody(t, rec)
		if strings.Contains(got, `zz-nobody`) || !strings.Contains(got, "run.json") {
			t.Errorf("the 404 = %q, want the drive path reduced to its file name", got)
		}
	})

	t.Run("a route of the API stays readable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpError(rec, http.StatusMethodNotAllowed, "POST /api/v1/runs/r1/cancel is not allowed here")
		if got := errorBody(t, rec); got != "POST /api/v1/runs/r1/cancel is not allowed here" {
			t.Errorf("an API route in a message was rewritten: %q", got)
		}
	})

	t.Run("a 5xx stays the generic message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.httpErrorFor(rec, httptest.NewRequest(http.MethodGet, "/x", nil), http.StatusInternalServerError, "load events: open %s: denied", runJSON)
		if got := errorBody(t, rec); strings.Contains(got, "run.json") || strings.Contains(got, work) {
			t.Errorf("the 500 carries a cause: %q", got)
		}
	})
}

// The diagnostic of a run that does not exist is the card's own reproduction:
// the answer is a 404 and says nothing of where the store lives.
func TestRunDiagnosticOfAMissingRunNamesNoStoreDirectory(t *testing.T) {
	work := t.TempDir()
	srv := New(Config{DisableAuth: true, WorkDir: work}, iterlog.New(iterlog.LevelError, nil))
	srv.handler = srv.mux

	rec := httptest.NewRecorder()
	srv.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/no-such-run/diagnostic", nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, spelling := range []string{work, filepath.ToSlash(work), strings.ToLower(work)} {
		if strings.Contains(body, spelling) {
			t.Errorf("the answer names the workspace %q: %s", spelling, body)
		}
	}
}
