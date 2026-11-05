package server

import (
	"net/http/httptest"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
)

func TestParseBotInclude(t *testing.T) {
	cases := map[string]botInclude{
		"":                         {},
		"/x?include=all":           {builtin: true, files: true},
		"/x?include=builtin":       {builtin: true},
		"/x?include=files,builtin": {builtin: true, files: true},
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

func TestAppendManifestlessBotsBuiltinsAndShadowing(t *testing.T) {
	s := &Server{}
	// A project bundle named like an embedded recipe wins.
	in := []botEntryView{{EntryWithSchema: botregistry.EntryWithSchema{Entry: botregistry.Entry{Name: "feature-dev"}}, Origin: "catalog"}}
	out := s.appendManifestlessBots(in, botInclude{builtin: true})
	seen := map[string]int{}
	for _, e := range out {
		seen[e.Name]++
		if e.Origin == botregistry.OriginBuiltin && (!e.Enabled || e.Editable) {
			t.Errorf("builtin %s must be enabled and read-only", e.Name)
		}
	}
	if seen["feature-dev"] != 1 {
		t.Fatalf("feature-dev listed %d times", seen["feature-dev"])
	}
	if seen["branch-improve-loop"] != 1 {
		t.Fatalf("embedded recipes missing: %v", seen)
	}
}
