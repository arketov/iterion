package server

import (
	"net/http"
	"path"
	"strings"

	"github.com/arketov/iterion/pkg/botregistry"
)

// botInclude selects the manifest-less bot sources GET /api/v1/bots adds on
// top of the discovered catalog. The default (no ?include=) stays the
// catalog + platform + tenant list, which every routing, launch and
// integration surface depends on; the studio's list/picker screens opt in
// with ?include=all so that the Bots page, the Home panel and the bot
// pickers show one and the same set. The only such source is the project's
// single .bot files: the binary's embedded recipes are deliberately not a
// listed bot source (owner decision 2026-11-05), so "builtin" is accepted and
// ignored for older clients.
type botInclude struct {
	files bool
}

// parseBotInclude reads the include query parameter: a comma-separated list
// of "files" or "all" ("builtin" is ignored).
func parseBotInclude(r *http.Request) botInclude {
	var inc botInclude
	for _, v := range r.URL.Query()["include"] {
		for _, tok := range strings.Split(v, ",") {
			switch strings.TrimSpace(tok) {
			case "all":
				inc.files = true
			case "files":
				inc.files = true
			}
		}
	}
	return inc
}

// appendManifestlessBots adds the project's single .bot files without a
// manifest (origin=file) to entries. A file already covered by a catalog
// entry's path is not listed twice. A file has no manifest, so no enabled flag
// to flip: it is reported enabled and not editable.
func (s *Server) appendManifestlessBots(entries []botEntryView, inc botInclude) []botEntryView {
	if !inc.files {
		return entries
	}
	names := make(map[string]struct{}, len(entries))
	paths := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		names[e.Name] = struct{}{}
		paths[strings.ToLower(path.Clean(strings.ReplaceAll(e.Path, "\\", "/")))] = struct{}{}
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
