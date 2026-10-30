package api

import (
	"context"
	"io"
	"net/http"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// Shared services (docs/api.md, section "Shared services"). The set of
// shared services of a project has one Compose project of its own; its
// operations are keyed "shared/<project>", apart from the instances.

func (s *Server) sharedRoutes() {
	s.handle("GET", "/projects/{p}/shared", authSession, s.handleSharedStatus)
	s.handle("POST", "/projects/{p}/shared", authSession, s.handleSharedAdd)
	s.handle("POST", "/projects/{p}/shared/up", authSession, s.sharedOperation("up"))
	s.handle("POST", "/projects/{p}/shared/down", authSession, s.sharedOperation("down"))
	s.handle("DELETE", "/projects/{p}/shared/services/{s}", authSession, s.handleSharedRemove)
	s.handle("POST", "/projects/{p}/shared/purge", authSession, s.handleSharedPurge)
	s.handle("PUT", "/projects/{p}/worktrees/{wt}/shared", authSession, s.handleSharedUse)
	s.handle("DELETE", "/projects/{p}/worktrees/{wt}/shared", authSession, s.handleSharedUnuse)
	s.handle("DELETE", "/projects/{p}/worktrees/{wt}/shared/{s}", authSession, s.handleSharedUnuse)
}

func sharedKey(project string) string { return "shared/" + project }

func sharedNotConfigured(project string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "shared_not_configured", Message: "project " + quote(project) + " shares no services",
		Hint: "declare them with POST /api/v1/projects/" + project + "/shared", Resource: &ResourceRef{Kind: "shared", Project: project}}
}

// requireShared checks that the project exists and shares services.
func (s *Server) requireShared(project string) error {
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	_, ok, err := s.eng.SharedSet(project)
	if err != nil {
		return err
	}
	if !ok {
		return sharedNotConfigured(project)
	}
	return nil
}

func sharedRef(project string) *ResourceRef { return &ResourceRef{Kind: "shared", Project: project} }

func (s *Server) handleSharedStatus(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	if err := s.requireShared(project); err != nil {
		return err
	}
	st, err := s.eng.SharedStatus(r.Context(), project)
	if err != nil {
		return fromEngine(err, sharedRef(project))
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleSharedAdd(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	var req struct {
		Services []string `json:"services"`
		From     string   `json:"from"`
		Publish  bool     `json:"publish"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	if len(req.Services) == 0 {
		return badRequest("services is required: the names of the services to share")
	}
	if _, err := s.eng.SharedAdd(r.Context(), project, req.Services, req.From, req.Publish); err != nil {
		return fromEngine(err, sharedRef(project))
	}
	st, err := s.eng.SharedStatus(r.Context(), project)
	if err != nil {
		return fromEngine(err, sharedRef(project))
	}
	s.hub.publish("state.changed", map[string]string{"project": project})
	return writeJSON(w, http.StatusCreated, st)
}

// sharedOperation starts `shared up` or `shared down` as an operation. For
// down, the protection comes first and synchronously: with running
// consumers and no matching confirm the answer is 409 and nothing starts.
func (s *Server) sharedOperation(kind string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, sess *session) error {
		project := r.PathValue("p")
		var req struct {
			Confirm string `json:"confirm"`
		}
		if kind == "down" {
			if err := decodeOptional(r, &req); err != nil {
				return err
			}
		}
		if err := s.requireShared(project); err != nil {
			return err
		}
		if kind == "down" {
			if _, err := s.eng.CheckSharedDown(r.Context(), project, req.Confirm); err != nil {
				return fromEngine(err, sharedRef(project))
			}
		}
		run := func(ctx context.Context, stdout, stderr io.Writer) (string, error) {
			if kind == "up" {
				if err := s.eng.SharedUp(ctx, project, stdout, stderr); err != nil {
					return "", err
				}
				return "The shared services of " + project + " are running.", nil
			}
			if err := s.eng.SharedDown(ctx, project, req.Confirm, stdout, stderr); err != nil {
				return "", err
			}
			return "Stopped the shared services of " + project + ". Their volumes were kept.", nil
		}
		op, replay, err := s.ops.start(startRequest{kind: "shared." + kind, project: project, key: sharedKey(project),
			session: sess.id, idemKey: r.Header.Get("Idempotency-Key"), run: run})
		if err != nil {
			return err
		}
		if !replay {
			s.cache.invalidate(project)
		}
		w.Header().Set("Location", apiBase+"/operations/"+op.ID)
		return writeJSON(w, http.StatusAccepted, op.view())
	}
}

func (s *Server) sharedBusy(project string) error {
	if op := s.ops.busy(sharedKey(project)); op != nil {
		e := errf(http.StatusConflict, "busy", "an operation is running on the shared services")
		e.Operation = &OperationRef{ID: op.ID, Kind: op.Kind}
		e.Hint = "wait for it or cancel it"
		return e
	}
	return nil
}

func (s *Server) handleSharedRemove(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	if err := s.requireShared(project); err != nil {
		return err
	}
	if err := s.sharedBusy(project); err != nil {
		return err
	}
	if err := s.eng.SharedRemove(r.Context(), project, []string{r.PathValue("s")}); err != nil {
		return fromEngine(err, sharedRef(project))
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	out := map[string]any{"removed": true, "service": r.PathValue("s"), "shared": false, "volumes_kept": true}
	if _, ok, _ := s.eng.SharedSet(project); ok {
		st, err := s.eng.SharedStatus(r.Context(), project)
		if err != nil {
			return fromEngine(err, sharedRef(project))
		}
		out["shared"], out["status"] = true, st
	}
	return writeJSON(w, http.StatusOK, out)
}

// handleSharedPurge deletes the data of the shared services. Besides the
// engine's own protections (containers stopped, typed confirmation) the API
// refuses while any instance is linked to them: shared_in_use.
func (s *Server) handleSharedPurge(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	if err := s.requireShared(project); err != nil {
		return err
	}
	if err := s.sharedBusy(project); err != nil {
		return err
	}
	consumers, err := s.eng.SharedConsumers(r.Context(), project)
	if err != nil {
		return fromEngine(err, sharedRef(project))
	}
	if len(consumers) > 0 {
		return fromEngine(&engine.SharedInUseError{Project: project, Action: "purge their data", Consumers: consumers}, sharedRef(project))
	}
	removed, err := s.eng.SharedPurge(r.Context(), project, req.Confirm)
	if err != nil {
		return fromEngine(err, sharedRef(project))
	}
	if removed == nil {
		removed = []engine.VolumeInfo{}
	}
	s.cache.invalidate(project)
	return writeJSON(w, http.StatusOK, struct {
		Compose string              `json:"compose_project"`
		Deleted []engine.VolumeInfo `json:"deleted"`
	}{project + "-shared", removed})
}

func (s *Server) handleSharedUse(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Use  []string `json:"use"`
		Join []string `json:"join"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	project := r.PathValue("p")
	if len(req.Use) == 0 {
		return badRequest("use is required: the shared services this instance should use")
	}
	if err := s.requireShared(project); err != nil {
		return err
	}
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	if err := s.eng.SharedUse(inst, req.Use, req.Join); err != nil {
		return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: r.PathValue("wt")})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project, "worktree": r.PathValue("wt")})
	return writeJSON(w, http.StatusOK, s.sharedUseView(inst))
}

// handleSharedUnuse serves DELETE .../shared (all services) and
// DELETE .../shared/{s}.
func (s *Server) handleSharedUnuse(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	var services []string
	if name := r.PathValue("s"); name != "" {
		services = []string{name}
	}
	if err := s.eng.SharedUnuse(inst, services); err != nil {
		return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: r.PathValue("wt")})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project, "worktree": r.PathValue("wt")})
	return writeJSON(w, http.StatusOK, s.sharedUseView(inst))
}

// sharedUseView says what an instance uses; it takes effect at the next up.
func (s *Server) sharedUseView(inst *engine.Instance) map[string]any {
	shared, join := []string{}, []string{}
	if v := s.settingsView(inst); v != nil {
		shared, join = v.Shared, v.Join
	}
	return map[string]any{"shared": shared, "join": join, "applies_on": "next_up"}
}
