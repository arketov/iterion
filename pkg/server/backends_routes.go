package server

import (
	"net/http"
	"time"

	"github.com/arketov/iterion/pkg/backend/detect"
)

// backendDetectTTL is how long the detect Report stays cached. Detection
// is cheap (env reads + filesystem stats) but the studio calls
// /api/backends/detect on every mount and could re-poll on focus, so we
// don't want to re-stat on every keystroke either.
const backendDetectTTL = 30 * time.Second

// handleBackendsDetect serves a snapshot of available LLM backends and
// providers. The route is read-only and reveals only booleans + source
// names (e.g. "ANTHROPIC_API_KEY") — never the credential values.
//
// `?force=1` invalidates the server-side cache before responding so the
// studio's refresh button can re-probe immediately after the user
// changes env / signs in elsewhere.
func (s *Server) handleBackendsDetect(w http.ResponseWriter, r *http.Request) {
	s.detectorOnce.Do(func() {
		s.detector = detect.NewCachedDetector(backendDetectTTL)
	})
	if r.URL.Query().Get("force") == "1" {
		// Refresh hook fires BEFORE invalidation so the next Detect()
		// picks up any env vars the hook just (un)set. Desktop registers
		// a hook that re-sources ~/.iterion/env.
		hadHook := s.OnForceRefresh != nil
		if hadHook {
			s.OnForceRefresh()
		}
		s.detector.Invalidate()
		s.logger.Info("backends/detect: force-refresh requested (hook fired: %v)", hadHook)
	}
	writeJSON(w, s.detector.Get(r.Context()))
}
