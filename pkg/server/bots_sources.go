package server

import (
	"net/http"
	"path"
	"strings"

	"github.com/arketov/iterion/bots"
	"github.com/arketov/iterion/pkg/botregistry"
)

// botInclude selects the manifest-less bot sources GET /api/v1/bots adds on
// top of the discovered catalog. The default (no ?include=) stays the
// catalog + platform + tenant list, which every routing, launch and
// integration surface depends on; the studio's list/picker screens opt in
// with ?include=all so that the Bots page, the Home panel and the bot
// pickers show one and the same set.
type botInclude struct {
	builtin bool
	files   bool
}

// parseBotInclude reads the include query parameter: a comma-separated list
// of "builtin", "files" or "all".
func parseBotInclude(r *http.Request) botInclude {
	var inc botInclude
	for _, v := range r.URL.Query()["include"] {
		for _, tok := range strings.Split(v, ",") {
			switch strings.TrimSpace(tok) {
			case "all":
				inc.builtin, inc.files = true, true
			case "builtin":
				inc.builtin = true
			case "files":
				inc.files = true
			}
		}
	}
	return inc
}

// appendManifestlessBots adds the embedded built-in recipes
// (origin=builtin) and the project's single .bot files without a manifest
// (origin=file) to entries. A name already present wins: a project bundle
// overriding an embedded recipe, or a file already covered by a catalog
// entry's path, is not listed twice. Neither kind has a manifest, so neither
// has an enabled flag to flip: both are reported enabled and not editable.
func (s *Server) appendManifestlessBots(entries []botEntryView, inc botInclude) []botEntryView {
	if !inc.builtin && !inc.files {
		return entries
	}
	names := make(map[string]struct{}, len(entries))
	paths := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		names[e.Name] = struct{}{}
		paths[strings.ToLower(path.Clean(strings.ReplaceAll(e.Path, "\\", "/")))] = struct{}{}
	}
	if inc.builtin {
		for _, p := range bots.List() {
			name := p
			if i := strings.IndexByte(p, '/'); i >= 0 {
				name = p[:i]
			}
			if _, dup := names[name]; dup {
				continue
			}
			names[name] = struct{}{}
			entries = append(entries, botEntryView{
				EntryWithSchema: botregistry.EntryWithSchema{Entry: botregistry.Entry{
					Name:            name,
					Path:            p,
					Enabled:         true,
					ManifestEnabled: true,
				}},
				Origin: botregistry.OriginBuiltin,
			})
		}
	}
	if inc.files {
		workDir := s.currentWorkDir()
		for _, e := range botregistry.DiscoverProjectFiles(workDir, s.effectivePathsFor(workDir)) {
			key := strings.ToLower(path.Clean(strings.ReplaceAll(e.Path, "\\", "/")))
			if _, dup := paths[key]; dup {
				continue
			}
			if _, dup := names[e.Name]; dup {
				// A file named like an existing bot: keep both, the file
				// under its workspace-relative path so names stay unique.
				e.Name = e.RelPath
			}
			names[e.Name] = struct{}{}
			entries = append(entries, botEntryView{
				EntryWithSchema: botregistry.EntryWithSchema{Entry: e},
				Origin:          botregistry.OriginFile,
			})
		}
	}
	return entries
}
