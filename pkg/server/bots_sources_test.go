package server

import (
	"net/http/httptest"
	"testing"
)

func TestParseBotInclude(t *testing.T) {
	cases := map[string]botInclude{
		"/x":                       {},
		"/x?include=all":           {files: true},
		"/x?include=files":         {files: true},
		"/x?include=builtin":       {},
		"/x?include=files,builtin": {files: true},
		"/x?include=nope":          {},
	}
	for url, want := range cases {
		if got := parseBotInclude(httptest.NewRequest("GET", url, nil)); got != want {
			t.Errorf("%q: got %+v want %+v", url, got, want)
		}
	}
}

func TestAppendManifestlessBotsDefaultUnchanged(t *testing.T) {
	s := &Server{}
	in := []botEntryView{{Origin: "catalog"}}
	if out := s.appendManifestlessBots(in, botInclude{}); len(out) != 1 {
		t.Fatalf("no include must not add entries, got %d", len(out))
	}
}

// The embedded recipes are not a listed bot source: even a client that still
// asks for them with ?include=all gets no origin=builtin entry.
func TestNoBuiltinBotIsEverListed(t *testing.T) {
	s := &Server{}
	for _, q := range []string{"/x?include=all", "/x?include=builtin", "/x?include=files,builtin"} {
		inc := parseBotInclude(httptest.NewRequest("GET", q, nil))
		for _, e := range s.appendManifestlessBots(nil, inc) {
			if e.Origin == "builtin" {
				t.Errorf("%s listed builtin bot %q", q, e.Name)
			}
		}
	}
}
