package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/botregistry"
	iterlog "github.com/arketov/iterion/pkg/log"
	"github.com/arketov/iterion/pkg/server/projects"
)

// A studio server serves one project folder: the board, the dispatcher and the
// coordinators are bound to it at boot and a hot-swap re-binds only half. The
// project routes must therefore refuse anything that would move or orphan the
// served folder, and leave the server exactly as it was. Not parallel: the
// project registry is process-wide.
func TestProjectRoutesNeverMoveTheServedFolder(t *testing.T) {
	type fixture struct {
		served, other, third string
		a, b                 projects.Project
	}
	cases := []struct {
		name     string
		prepare  func(t *testing.T, f fixture, reg *projects.Config)
		method   string
		target   func(f fixture) string
		body     func(f fixture) string
		pathID   func(f fixture) string
		want     int
		wantCode string
		check    func(t *testing.T, f fixture, reg *projects.Config)
	}{
		{
			name:   "switch to another folder",
			method: http.MethodPost, target: func(fixture) string { return "/api/projects/switch" },
			body: func(f fixture) string { return `{"id":"` + f.b.ID + `"}` },
			want: http.StatusConflict, wantCode: "project_switch_unsupported",
			check: func(t *testing.T, f fixture, reg *projects.Config) {
				if cur := reg.Current(); cur == nil || cur.ID != f.a.ID {
					t.Fatalf("registry current moved: %+v", cur)
				}
			},
		},
		{
			name:   "switch to the served folder",
			method: http.MethodPost, target: func(fixture) string { return "/api/projects/switch" },
			body: func(f fixture) string { return `{"id":"` + f.a.ID + `"}` },
			want: http.StatusOK,
		},
		{
			name:   "add another folder",
			method: http.MethodPost, target: func(fixture) string { return "/api/projects" },
			body: func(f fixture) string { return `{"dir":` + jsonString(f.third) + `}` },
			want: http.StatusConflict, wantCode: "project_switch_unsupported",
			check: func(t *testing.T, f fixture, reg *projects.Config) {
				for _, p := range reg.RecentProjects {
					if botregistry.PathKey(p.Dir) == botregistry.PathKey(f.third) {
						t.Fatalf("refused add still registered %q", p.Dir)
					}
				}
			},
		},
		{
			name:   "remove the served folder",
			method: http.MethodDelete, target: func(f fixture) string { return "/api/projects/" + f.a.ID },
			pathID: func(f fixture) string { return f.a.ID },
			want:   http.StatusConflict, wantCode: "project_switch_unsupported",
			check: func(t *testing.T, f fixture, reg *projects.Config) {
				if reg.ByID(f.a.ID) == nil {
					t.Fatal("refused remove dropped the served project")
				}
			},
		},
		{
			name: "remove another folder the registry calls current",
			prepare: func(t *testing.T, f fixture, reg *projects.Config) {
				reg.SetCurrent(f.b.ID)
			},
			method: http.MethodDelete, target: func(f fixture) string { return "/api/projects/" + f.b.ID },
			pathID: func(f fixture) string { return f.b.ID },
			want:   http.StatusNoContent,
			check: func(t *testing.T, f fixture, reg *projects.Config) {
				if reg.ByID(f.b.ID) != nil {
					t.Fatal("remove left the project in the registry")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := fixture{
				served: filepath.Join(root, "a"),
				other:  filepath.Join(root, "b"),
				third:  filepath.Join(root, "c"),
			}
			for _, d := range []string{f.served, f.other, f.third} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			srv := New(Config{
				WorkDir:                 f.served,
				StoreDir:                filepath.Join(f.served, ".iterion"),
				SkipProjectRegistration: true,
			}, iterlog.New(iterlog.LevelError, nil))
			shutdownOnCleanup(t, srv)

			reg, err := projects.Load()
			if err != nil {
				t.Fatal(err)
			}
			f.b = reg.AddOrTouch(f.other)
			f.a = reg.AddOrTouch(f.served)
			if tc.prepare != nil {
				tc.prepare(t, f, reg)
			}
			if err := reg.Save(); err != nil {
				t.Fatal(err)
			}
			runsBefore := srv.runs

			var body *strings.Reader
			if tc.body != nil {
				body = strings.NewReader(tc.body(f))
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, tc.target(f), body)
			req.Header.Set("Content-Type", "application/json")
			if tc.pathID != nil {
				req.SetPathValue("id", tc.pathID(f))
			}
			rec := httptest.NewRecorder()
			switch {
			case tc.method == http.MethodDelete:
				srv.handleRemoveProject(rec, req)
			case strings.HasSuffix(tc.target(f), "/switch"):
				srv.handleSwitchProject(rec, req)
			default:
				srv.handleAddProject(rec, req)
			}

			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.wantCode != "" {
				var got map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode %q: %v", rec.Body.String(), err)
				}
				if got["error_code"] != tc.wantCode {
					t.Fatalf("error_code %q, want %q (%v)", got["error_code"], tc.wantCode, got)
				}
				if strings.Contains(rec.Body.String(), f.other) || strings.Contains(rec.Body.String(), f.third) {
					t.Fatalf("refusal names a host path: %s", rec.Body.String())
				}
			}
			if tc.check != nil {
				after, err := projects.Load()
				if err != nil {
					t.Fatal(err)
				}
				tc.check(t, f, after)
			}
			if botregistry.PathKey(srv.currentWorkDir()) != botregistry.PathKey(f.served) {
				t.Fatalf("served folder moved to %q", srv.currentWorkDir())
			}
			if srv.runs != runsBefore {
				t.Fatal("the run service was rebuilt")
			}
		})
	}
}
