package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/update"
)

// updateManager is the state of "check for a new version and install it".
// One installation runs at a time; the page polls GET /update while it does.
type updateManager struct {
	mu       sync.Mutex
	state    string // idle, downloading, installed, error
	done     int64
	total    int64
	err      string
	errCode  string
	installd string // the tag that was installed
}

// UpdateStatus is the answer of GET /update.
type UpdateStatus struct {
	update.Info
	// Enabled is whether Theo looks by itself (the update-check setting).
	Enabled bool `json:"enabled"`
	// Notify is whether the desktop app shows a notification (update-notify).
	Notify bool `json:"notify"`
	// Channel is stable, or rc to be offered release candidates.
	Channel string `json:"channel"`
	// Desktop is true in the desktop app, which can restart itself.
	Desktop bool `json:"desktop"`
	// CanInstall is false when this copy of Theo cannot replace itself;
	// WhyNot says why (not_writable or unmanaged).
	CanInstall bool   `json:"can_install"`
	WhyNot     string `json:"why_not,omitempty"`
	// State is idle, downloading, installed or error.
	State string `json:"state"`
	// Done and Total are the bytes downloaded so far.
	Done  int64 `json:"done,omitempty"`
	Total int64 `json:"total,omitempty"`
	// Installed is the tag that was installed, once State is installed.
	Installed string `json:"installed,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func (s *Server) checker() update.Checker {
	c := update.Checker{Store: s.eng.Prefs(), Current: engine.Version, Desktop: s.cfg.Desktop, List: s.cfg.UpdateList}
	return c
}

func (s *Server) updateTarget() update.Target {
	if s.cfg.UpdateTarget != nil {
		return *s.cfg.UpdateTarget
	}
	// Taken when the server started: after an update the running program's own
	// file is gone, and asking the system again would name "theo (deleted)".
	return s.target
}

func (s *Server) updateStatus() UpdateStatus {
	c := s.checker()
	st := UpdateStatus{Info: c.Info(), Enabled: c.Enabled(), Notify: c.Store.Get("update-notify") != "off", Channel: "stable", Desktop: s.cfg.Desktop && s.cfg.Restart != nil, CanInstall: true}
	if c.IncludeRC() {
		st.Channel = "rc"
	}
	if _, err := update.PlanFor(s.updateTarget(), "v0.0.0"); err != nil {
		st.CanInstall, st.WhyNot = false, "unmanaged"
	} else if err := update.WritableFor(s.updateTarget()); err != nil {
		st.CanInstall, st.WhyNot = false, "not_writable"
	}
	m := &s.updates
	m.mu.Lock()
	defer m.mu.Unlock()
	st.State = m.state
	if st.State == "" {
		st.State = "idle"
	}
	st.Done, st.Total, st.Installed, st.Error, st.ErrorCode = m.done, m.total, m.installd, m.err, m.errCode
	return st
}

func (s *Server) handleUpdateGet(w http.ResponseWriter, _ *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, s.updateStatus())
}

// handleUpdateCheck looks now, whatever the setting says: the person asked.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request, _ *session) error {
	st := s.updateStatus()
	if _, err := s.checker().Check(r.Context(), true); err != nil {
		st = s.updateStatus()
		st.Error, st.ErrorCode = "Could not reach GitHub: "+err.Error(), "check_failed"
		return writeJSON(w, http.StatusOK, st)
	}
	return writeJSON(w, http.StatusOK, s.updateStatus())
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		AutoCheck *bool  `json:"auto_check"`
		Notify    *bool  `json:"notify"`
		Channel   string `json:"channel"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.AutoCheck == nil && req.Notify == nil && req.Channel == "" {
		return badRequest("send auto_check, notify and/or channel")
	}
	if req.Channel != "" && req.Channel != "stable" && req.Channel != "rc" {
		return badRequest("channel must be stable or rc")
	}
	if req.AutoCheck != nil {
		v := "off"
		if *req.AutoCheck {
			v = "on"
		}
		if err := s.eng.Prefs().Set("update-check", v); err != nil {
			return err
		}
	}
	if req.Notify != nil {
		v := "off"
		if *req.Notify {
			v = "on"
		}
		if err := s.eng.Prefs().Set("update-notify", v); err != nil {
			return err
		}
	}
	if req.Channel != "" {
		if err := s.eng.Prefs().Set("update-channel", req.Channel); err != nil {
			return err
		}
		// The answer of the other channel may differ: ask again, if looking is on.
		if s.checker().Enabled() {
			_, _ = s.checker().Check(r.Context(), true)
		}
	}
	return writeJSON(w, http.StatusOK, s.updateStatus())
}

func (s *Server) handleUpdateDismiss(w http.ResponseWriter, _ *http.Request, _ *session) error {
	if err := s.checker().Dismiss(); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.updateStatus())
}

// handleUpdateInstall starts downloading and installing the version the last
// look found. It answers at once; GET /update says how far it is.
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request, _ *session) error {
	c := s.checker()
	info := c.Info()
	if !info.Available {
		return errf(http.StatusConflict, "no_update", "there is no new version to install")
	}
	m := &s.updates
	m.mu.Lock()
	if m.state == "downloading" {
		m.mu.Unlock()
		return errf(http.StatusConflict, "update_running", "an update is already being installed")
	}
	m.state, m.done, m.total, m.err, m.errCode, m.installd = "downloading", 0, 0, "", "", ""
	m.mu.Unlock()
	go s.installUpdate(info.Tag)
	return writeJSON(w, http.StatusAccepted, s.updateStatus())
}

func (s *Server) installUpdate(tag string) {
	m := &s.updates
	fail := func(code string, err error) {
		m.mu.Lock()
		m.state, m.err, m.errCode = "error", err.Error(), code
		m.mu.Unlock()
		s.log.Printf("update to %s failed: %v", tag, err)
	}
	ctx, cancel := context.WithTimeout(s.workCtx, 15*time.Minute)
	defer cancel()
	list := s.cfg.UpdateList
	if list == nil {
		list = update.ListReleases
	}
	rels, err := list(ctx)
	if err != nil {
		fail("check_failed", err)
		return
	}
	var rel update.Release
	for _, r := range rels {
		if r.Tag == tag {
			rel = r
		}
	}
	if rel.Tag == "" {
		fail("check_failed", errors.New("release "+tag+" is no longer listed"))
		return
	}
	_, err = update.Apply(ctx, rel, s.updateTarget(), func(done, total int64) {
		m.mu.Lock()
		m.done, m.total = done, total
		m.mu.Unlock()
	})
	switch {
	case errors.Is(err, update.ErrNotWritable):
		fail("not_writable", err)
	case errors.Is(err, update.ErrUnmanaged):
		fail("unmanaged", err)
	case errors.Is(err, update.ErrChecksum):
		fail("checksum", err)
	case err != nil:
		fail("install_failed", err)
	default:
		m.mu.Lock()
		m.state, m.installd = "installed", tag
		m.mu.Unlock()
		s.log.Printf("updated to %s", tag)
	}
}

// handleUpdateRestart restarts the desktop app on the version just installed.
func (s *Server) handleUpdateRestart(w http.ResponseWriter, _ *http.Request, _ *session) error {
	if s.cfg.Restart == nil {
		return errf(http.StatusConflict, "no_restart", "only the desktop app restarts itself: close Theo and open it again")
	}
	s.updates.mu.Lock()
	installed := s.updates.state == "installed"
	s.updates.mu.Unlock()
	if !installed {
		return errf(http.StatusConflict, "nothing_installed", "no new version has been installed yet")
	}
	w.WriteHeader(http.StatusNoContent)
	go func() {
		time.Sleep(200 * time.Millisecond) // let the answer reach the page
		s.cfg.Restart()
	}()
	return nil
}

// watchUpdates looks for a new version when Theo starts and then every
// update.Interval, when the person allows it, so the page has the answer.
func (s *Server) watchUpdates(ctx context.Context) {
	look := func() {
		c := s.checker()
		if c.Enabled() && c.Due() {
			lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_, _ = c.Check(lctx, false)
		}
	}
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return
	}
	look()
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			look()
		case <-ctx.Done():
			return
		}
	}
}
