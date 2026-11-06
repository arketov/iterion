package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
	iterlog "github.com/arketov/iterion/pkg/log"
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

func writeSourceFixture(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("workflow w:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileOriginNames(entries []botEntryView) []string {
	out := []string{}
	for _, e := range entries {
		if e.Origin == botregistry.OriginFile {
			out = append(out, e.RelPath)
		}
	}
	return out
}

// origin=file rows are the single .bot files of the project root and nothing
// else: not a file deeper in the tree, not a nested clone, not a fragment.
func TestAppendManifestlessBotsRootFilesOnly(t *testing.T) {
	root := t.TempDir()
	writeSourceFixture(t, root, "dev-flow.bot")
	writeSourceFixture(t, root, "flows/deep.bot")
	writeSourceFixture(t, root, "build-desktop/src/bots/y/main.bot")
	writeSourceFixture(t, root, "testdata/input_mapping/a.bot")
	writeSourceFixture(t, root, "bots/x/lib/nodes.bot")
	s := &Server{cfg: Config{WorkDir: root}}
	got := fileOriginNames(s.appendManifestlessBots(nil, botInclude{files: true}))
	if want := []string{"dev-flow.bot"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// --bots-path . makes the project folder a catalog root: the catalog lists
// its files, so origin=file adds none.
func TestAppendManifestlessBotsNoDuplicateWhenTheProjectIsACatalogRoot(t *testing.T) {
	root := t.TempDir()
	writeSourceFixture(t, root, "a.bot")
	s := &Server{cfg: Config{WorkDir: root, Bots: BotsConfig{Paths: []string{root}}}}
	if got := fileOriginNames(s.appendManifestlessBots(nil, botInclude{files: true})); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// A file the catalog already lists by path is not listed twice, whatever the
// spelling of the path.
func TestAppendManifestlessBotsSkipsAPathTheCatalogHas(t *testing.T) {
	root := t.TempDir()
	writeSourceFixture(t, root, "a.bot")
	s := &Server{cfg: Config{WorkDir: root}}
	in := []botEntryView{{
		EntryWithSchema: botregistry.EntryWithSchema{Entry: botregistry.Entry{
			Name: "a", Path: filepath.Join(root, "sub", "..", "a.bot"),
		}},
		Origin: "catalog",
	}}
	if got := fileOriginNames(s.appendManifestlessBots(in, botInclude{files: true})); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// A cloud server's WorkDir is the platform's own tree. The scan is switched off
// for a caller with a team in the route, but a cloud user with no team (and a
// service token) reached it: ?include=all walked the server's tree and answered
// the names of its .bot files. Nothing on a cloud server scans, whoever asks.
func TestAppendManifestlessBotsNeverScansACloudServer(t *testing.T) {
	root := t.TempDir()
	writeSourceFixture(t, root, "platform-internal.bot")
	s := &Server{cfg: Config{Mode: "cloud", WorkDir: root}}
	if got := fileOriginNames(s.appendManifestlessBots(nil, botInclude{files: true})); len(got) != 0 {
		t.Fatalf("a cloud server listed its own tree: %v", got)
	}
}

// The same through the route: a caller with no team on a cloud server, asking
// for ?include=all, gets no origin=file row and no name from the server's tree;
// the same request on a local server lists the file, so the test cannot pass
// by the scan being broken.
func TestBotsListRoute_IncludeAllScansOnlyALocalServer(t *testing.T) {
	botregistry.ClearSchemaCache()
	for _, tc := range []struct {
		mode      string
		wantFiles []string
	}{
		{"cloud", []string{}},
		{"", []string{"platform-internal.bot"}},
	} {
		root := t.TempDir()
		writeSourceFixture(t, root, "platform-internal.bot")
		srv := New(Config{DisableAuth: true, WorkDir: root, Mode: tc.mode}, iterlog.New(iterlog.LevelError, nil))
		srv.handler = srv.mux

		rec := httptest.NewRecorder()
		srv.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/bots?include=all", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("mode %q: status = %d; body=%s", tc.mode, rec.Code, rec.Body.String())
		}
		var resp struct {
			Bots []struct {
				Origin  string `json:"origin"`
				RelPath string `json:"rel_path"`
			} `json:"bots"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("mode %q: decode: %v; body=%s", tc.mode, err, rec.Body.String())
		}
		got := []string{}
		for _, b := range resp.Bots {
			if b.Origin == botregistry.OriginFile {
				got = append(got, b.RelPath)
			}
		}
		if !reflect.DeepEqual(got, tc.wantFiles) {
			t.Errorf("mode %q: origin=file rows = %v, want %v", tc.mode, got, tc.wantFiles)
		}
		if tc.mode == "cloud" && strings.Contains(rec.Body.String(), "platform-internal") {
			t.Errorf("mode %q: the body names a file of the server's tree: %s", tc.mode, rec.Body.String())
		}
	}
}
