package server

import (
	"errors"
	"net/http"

	"github.com/arketov/iterion/pkg/auth"
	"github.com/arketov/iterion/pkg/runview"
	"github.com/arketov/iterion/pkg/store"
)

// discardBranchRequest is the body of POST /api/runs/{id}/discard-branch.
// Force accepts the loss of commits that are not merged anywhere.
type discardBranchRequest struct {
	Force bool `json:"force,omitempty"`
}

// handleDiscardRunBranch deletes a finished run's storage branch
// (iterion/run/<id>) from the local repository. The branch is the only GC
// guard of the run's commits, so a branch whose work is not merged is
// refused with the coded 409 `branch_unmerged` until the caller repeats the
// request with force. Every other refusal (live run, moved or foreign
// branch, no local repository) is a plain 409; an unknown run is a 404.
func (s *Server) handleDiscardRunBranch(w http.ResponseWriter, r *http.Request) {
	if !s.requireSafeOrigin(w, r) {
		return
	}
	if s.rejectCrossStoreWrite(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.httpErrorFor(w, r, http.StatusBadRequest, "missing run id")
		return
	}
	var req discardBranchRequest
	if err := readJSON(r, &req); err != nil {
		s.httpErrorFor(w, r, http.StatusBadRequest, "invalid request: %v", err)
		return
	}
	if err := s.runs.DiscardBranchCtx(r.Context(), id, req.Force); err != nil {
		switch {
		case errors.Is(err, runview.ErrBranchUnmerged):
			s.httpErrorCode(w, r, http.StatusConflict, "branch_unmerged", "discard branch: %v", err)
		case errors.Is(err, store.ErrRunNotFound) || errors.Is(err, store.ErrRunDeleted):
			s.httpErrorFor(w, r, http.StatusNotFound, "discard branch: %v", err)
		default:
			s.httpErrorFor(w, r, http.StatusConflict, "discard branch: %v", err)
		}
		return
	}
	ident, _ := auth.FromContext(r.Context())
	s.auditTenant(r, ident.TeamID, "run.branch_discarded", "run", id, nil)
	w.WriteHeader(http.StatusNoContent)
}
