package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// lifecycleRoutes are the routes of forgotten work (ADR-0016): the state of
// every workspace, cleaning, parking, resuming and keeping.
func (s *Server) lifecycleRoutes() {
	s.handle("GET", "/projects/{p}/lifecycle", authSession, s.handleLifecycle)
	s.handle("GET", "/projects/{p}/clean", authSession, s.handleCleanPlan)
	s.handle("POST", "/projects/{p}/clean", authSession, s.handleClean)
	s.handle("POST", "/projects/{p}/resume", authSession, s.handleResume)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/park", authSession, s.handlePark)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/keep", authSession, s.handleKeep)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/open", authSession, s.handleOpenOptions)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/open", authSession, s.handleOpen)
}

// LifecycleView is one workspace in GET /projects/{p}/lifecycle.
type LifecycleView struct {
	ID string `json:"id"`
	engine.WorktreeState
}

func (s *Server) handleLifecycle(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	report, err := s.eng.WorktreeStates(r.Context(), project, engine.StatesOptions{Size: r.URL.Query().Get("size") == "1"})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: project})
	}
	views := make([]LifecycleView, len(report.Worktrees))
	for i, wt := range report.Worktrees {
		views[i] = LifecycleView{ID: worktreeID(wt.Path), WorktreeState: wt}
	}
	return writeJSON(w, http.StatusOK, map[string]any{
		"project": report.Project, "inactive_after_days": report.InactiveAfterDays,
		"worktrees": views, "parked": report.Parked, "notes": nonNil(report.Notes),
	})
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Server) handleCleanPlan(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	report, err := s.eng.Clean(r.Context(), project, engine.CleanOptions{})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: project})
	}
	return writeJSON(w, http.StatusOK, report)
}

// handleClean closes what the plan marks as closable. It needs the project
// name as confirmation, like `theo worktree clean --confirm`.
func (s *Server) handleClean(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	var req struct {
		Down           bool   `json:"down"`
		DeleteBranches bool   `json:"delete_branches"`
		Images         bool   `json:"images"`
		Confirm        string `json:"confirm"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	p, err := s.eng.Project(project)
	if err != nil {
		return projectNotFound(project)
	}
	if req.Confirm != p.Name {
		e := errf(http.StatusConflict, "confirmation_required", "closing worktrees needs the project name as confirmation")
		e.Confirm = &Confirm{Expected: p.Name, Prompt: "Type the project name to close these worktrees"}
		return e
	}
	report, err := s.eng.Clean(r.Context(), p.Name, engine.CleanOptions{Down: req.Down, DeleteBranches: req.DeleteBranches, Images: req.Images, Apply: true})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: p.Name})
	}
	s.cache.invalidate(p.Name)
	s.hub.publish("state.changed", map[string]string{"project": p.Name})
	return writeJSON(w, http.StatusOK, report)
}

func (s *Server) handlePark(w http.ResponseWriter, r *http.Request, _ *session) error {
	project, id := r.PathValue("p"), r.PathValue("wt")
	var req struct {
		Note    string `json:"note"`
		Confirm string `json:"confirm"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	wt, err := s.resolveWorktree(r.Context(), project, id)
	if err != nil {
		return err
	}
	if op := s.ops.busy(instanceKey(project, id)); op != nil {
		e := errf(http.StatusConflict, "busy", "an operation is running on this instance")
		e.Operation = &OperationRef{ID: op.ID, Kind: op.Kind}
		return e
	}
	res, err := s.eng.Park(r.Context(), project, wt.Path, engine.ParkOptions{Note: req.Note, Confirm: req.Confirm})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: id})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	return writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	var req struct {
		Branch string `json:"branch"`
		Copy   bool   `json:"copy"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Branch) == "" {
		return badRequest("branch is required")
	}
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	added, err := s.eng.Resume(r.Context(), project, req.Branch, engine.ResumeOptions{Copy: req.Copy})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: project})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	return writeJSON(w, http.StatusOK, struct {
		ID string `json:"id"`
		*engine.AddedWorktree
	}{worktreeID(added.Path), added})
}

func (s *Server) handleKeep(w http.ResponseWriter, r *http.Request, _ *session) error {
	project, id := r.PathValue("p"), r.PathValue("wt")
	var req struct {
		Until string `json:"until"` // YYYY-MM-DD, empty: until cleared
		Note  string `json:"note"`
		Clear bool   `json:"clear"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	wt, err := s.resolveWorktree(r.Context(), project, id)
	if err != nil {
		return err
	}
	var until time.Time
	if req.Until != "" {
		d, err := time.ParseInLocation("2006-01-02", req.Until, time.Local)
		if err != nil {
			return badRequest("until must be a date like 2026-12-01")
		}
		until = d.Add(24*time.Hour - time.Second)
	}
	rec, err := s.eng.Keep(r.Context(), project, wt.Path, engine.KeepOptions{Until: until, Note: req.Note, Clear: req.Clear})
	if err != nil {
		return badRequest(err.Error())
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	return writeJSON(w, http.StatusOK, rec)
}
