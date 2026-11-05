package api

import (
	"net/http"

	"github.com/ElMaxter99/Theo/internal/opener"
)

// OpenOption is one way to open a worktree folder and whether it works now.
type OpenOption struct {
	Target    string `json:"target"` // terminal, editor or files
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) handleOpenOptions(w http.ResponseWriter, r *http.Request, _ *session) error {
	wt, err := s.resolveWorktree(r.Context(), r.PathValue("p"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	opts := []OpenOption{}
	for _, t := range opener.Targets {
		ok, why := opener.Available(s.eng.Prefs(), t, wt.Path)
		opts = append(opts, OpenOption{Target: t, Available: ok, Reason: why})
	}
	return writeJSON(w, http.StatusOK, map[string]any{"options": opts})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Target string `json:"target"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	known := false
	for _, t := range opener.Targets {
		known = known || t == req.Target
	}
	if !known {
		return badRequest("target must be terminal, editor or files")
	}
	wt, err := s.resolveWorktree(r.Context(), r.PathValue("p"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	label, err := opener.Open(s.eng.Prefs(), req.Target, wt.Path)
	if err != nil {
		return errf(http.StatusUnprocessableEntity, "open_failed", err.Error())
	}
	return writeJSON(w, http.StatusOK, map[string]any{"opened": true, "target": req.Target, "tool": label})
}
