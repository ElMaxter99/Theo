package api

import "net/http"

// notifyModes are the values of the notify.agent preference.
var notifyModes = map[string]bool{"off": true, "waiting": true, "all": true}

func (s *Server) notifyMode() string {
	m := s.eng.Prefs().Get("notify.agent")
	if !notifyModes[m] {
		return "off"
	}
	return m
}

func (s *Server) handleNotifyGet(w http.ResponseWriter, _ *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, map[string]any{"mode": s.notifyMode()})
}

func (s *Server) handleNotifySet(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !notifyModes[req.Mode] {
		return badRequest("mode must be off, waiting or all")
	}
	if err := s.eng.Prefs().Set("notify.agent", req.Mode); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"mode": req.Mode})
}
