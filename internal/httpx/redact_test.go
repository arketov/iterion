package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// maskRoot stands in for the server's redactor: any text naming the secret
// directory loses it. The tests are about WHERE the redactor is applied, not
// what it recognises (internal/pathredact owns that).
func maskRoot(s string) string { return strings.ReplaceAll(s, "/srv/secret-store", "<store>") }

func installMask(t *testing.T) {
	t.Helper()
	SetErrorRedactor(maskRoot)
	t.Cleanup(func() { SetErrorRedactor(nil) })
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

// The leak this guards: a handler formats `err.Error()` of a file operation
// into a 4xx and the caller reads the host's directory layout.
func TestWriteError_4xxMessageGoesThroughTheRedactor(t *testing.T) {
	installMask(t)
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "run_not_found", "load diagnostic: open /srv/secret-store/runs/r1/run.json: no such file")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := decodeBody(t, rec)
	if msg, _ := body["error"].(string); strings.Contains(msg, "/srv/secret-store") || !strings.Contains(msg, "<store>/runs/r1/run.json") {
		t.Errorf("error = %q, want the store directory redacted and the rest kept", msg)
	}
	if body["error_code"] != "run_not_found" {
		t.Errorf("error_code = %v, want it kept beside the redacted message", body["error_code"])
	}
}

func TestWriteErrorJSON_4xxMapsOfBothShapesAreRedactedAndNotEditedInPlace(t *testing.T) {
	installMask(t)
	byString := map[string]string{"error": "stat /srv/secret-store/x", "error_code": "c"}
	byAny := map[string]any{"error": "stat /srv/secret-store/y", "blockers": []string{"b"}}

	rec := httptest.NewRecorder()
	WriteErrorJSON(rec, http.StatusBadRequest, byString)
	if got := decodeBody(t, rec)["error"]; got != "stat <store>/x" {
		t.Errorf("map[string]string error = %v", got)
	}
	rec = httptest.NewRecorder()
	WriteErrorJSON(rec, http.StatusConflict, byAny)
	body := decodeBody(t, rec)
	if body["error"] != "stat <store>/y" {
		t.Errorf("map[string]any error = %v", body["error"])
	}
	if _, ok := body["blockers"]; !ok {
		t.Errorf("a 4xx keeps its actionable payload, body = %v", body)
	}
	// The caller's maps are its own: a redaction that wrote into them would
	// change a payload the caller logs or reuses.
	if byString["error"] != "stat /srv/secret-store/x" || byAny["error"] != "stat /srv/secret-store/y" {
		t.Errorf("the payload maps were edited in place: %v %v", byString, byAny)
	}
}

// A 5xx never carried the message; the redactor must not change that.
func TestWriteErrorJSON_5xxStaysGeneric(t *testing.T) {
	installMask(t)
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusInternalServerError, "", "open /srv/secret-store/x: denied")
	if got := decodeBody(t, rec)["error"]; got != InternalServerError {
		t.Errorf("5xx error = %v, want the generic message", got)
	}
}

// Without an installed redactor (a process that is not the studio server) the
// message goes out untouched, so the dispatcher and hostmcp keep their answers.
func TestWriteError_NoRedactorLeavesTheMessage(t *testing.T) {
	SetErrorRedactor(nil)
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusBadRequest, "", "stat /srv/secret-store/x")
	if got := decodeBody(t, rec)["error"]; got != "stat /srv/secret-store/x" {
		t.Errorf("error = %v, want it unchanged", got)
	}
	if got := RedactError("a /srv/secret-store/b"); got != "a /srv/secret-store/b" {
		t.Errorf("RedactError without a redactor = %q", got)
	}
}

func TestRedactError_AppliesTheInstalledRedactor(t *testing.T) {
	installMask(t)
	if got := RedactError("a /srv/secret-store/b"); got != "a <store>/b" {
		t.Errorf("RedactError = %q", got)
	}
}
