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

// skipFocused reports the notify.skip-focused preference: no notices while the
// desktop window is in front.
func (s *Server) skipFocused() bool { return s.eng.Prefs().Get("notify.skip-focused") == "on" }

func (s *Server) handleNotifyGet(w http.ResponseWriter, _ *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, map[string]any{"mode": s.notifyMode(), "skip_focused": s.skipFocused()})
}

func (s *Server) handleNotifySet(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Mode        string `json:"mode"`
		SkipFocused *bool  `json:"skip_focused"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Mode == "" && req.SkipFocused == nil {
		return badRequest("send mode and/or skip_focused")
	}
	if req.Mode != "" && !notifyModes[req.Mode] {
		return badRequest("mode must be off, waiting or all")
	}
	if req.Mode != "" {
		if err := s.eng.Prefs().Set("notify.agent", req.Mode); err != nil {
			return err
		}
	}
	if req.SkipFocused != nil {
		v := "off"
		if *req.SkipFocused {
			v = "on"
		}
		if err := s.eng.Prefs().Set("notify.skip-focused", v); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, map[string]any{"mode": s.notifyMode(), "skip_focused": s.skipFocused()})
}
