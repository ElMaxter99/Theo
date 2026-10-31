package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// routesTable registers every route. Handlers are never mounted on the mux
// directly: register wraps them in guard, and TestEveryRouteIsGuarded checks
// each entry of the table.
func (s *Server) routesTable() {
	// Exempt from the session: the exchange itself, and the control channel.
	s.handle("POST", "/session", authPublic, s.handleExchange)
	s.handle("POST", "/launch", authControl, s.handleLaunch)

	s.handle("GET", "/session", authSession, s.handleSession)
	s.handle("GET", "/meta", authSession, s.handleMeta)
	s.handle("GET", "/doctor", authSession, s.handleDoctor)
	s.stream("GET", "/events", s.handleEvents)
	s.handle("POST", "/events/focus", authSession, s.handleFocus)
	s.handle("GET", "/fs/browse", authSession, s.handleBrowse)

	s.handle("GET", "/projects", authSession, s.handleProjects)
	s.handle("POST", "/projects", authSession, s.handleAddProject)
	s.handle("PATCH", "/projects/{p}", authSession, s.handlePatchProject)
	s.handle("DELETE", "/projects/{p}", authSession, s.handleRemoveProject)
	s.handle("GET", "/projects/{p}/overview", authSession, s.handleOverview)
	s.handle("GET", "/projects/{p}/branches", authSession, s.handleBranches)

	s.handle("GET", "/projects/{p}/worktrees", authSession, s.handleWorktrees)
	s.handle("POST", "/projects/{p}/worktrees", authSession, s.handleCreateWorktree)
	s.handle("GET", "/projects/{p}/worktrees/{wt}", authSession, s.handleWorktree)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/remove", authSession, s.handleRemoveWorktree)

	for _, kind := range []string{"up", "down", "restart"} {
		s.handle("POST", "/projects/{p}/worktrees/{wt}/"+kind, authSession, s.lifecycle(kind))
	}
	s.handle("GET", "/projects/{p}/worktrees/{wt}/services", authSession, s.handleServices)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/ports", authSession, s.handlePorts)
	s.stream("GET", "/projects/{p}/worktrees/{wt}/logs", s.handleLogs)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/config", authSession, s.handleConfig)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/env", authSession, s.handleEnv)
	s.handle("PUT", "/projects/{p}/worktrees/{wt}/env/{name}", authSession, s.handleSetEnv)
	s.handle("DELETE", "/projects/{p}/worktrees/{wt}/env/{name}", authSession, s.handleUnsetEnv)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/env/{name}/reveal", authSession, s.handleReveal)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/settings", authSession, s.handleSettings)
	s.handle("PATCH", "/projects/{p}/worktrees/{wt}/settings", authSession, s.handlePatchSettings)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/volumes", authSession, s.handleVolumes)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/volumes/purge", authSession, s.handlePurge)
	s.handle("GET", "/projects/{p}/worktrees/{wt}/history", authSession, s.handleHistory)

	s.sharedRoutes()
	s.reconcileRoutes()

	s.handle("GET", "/operations", authSession, s.handleOperations)
	s.handle("GET", "/operations/{id}", authSession, s.handleOperation)
	s.handle("POST", "/operations/{id}/cancel", authSession, s.handleCancel)
	s.stream("GET", "/operations/{id}/events", s.handleOperationEvents)

	// The interface itself: static, no data, no session (it has to load
	// before it can exchange its nonce). Host is still checked.
	s.mux.HandleFunc("/", s.staticHandler())
}

// --- session, launch ---

func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Nonce string `json:"nonce"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	sess, ok := s.auth.exchange(req.Nonce)
	if !ok {
		return errf(http.StatusUnauthorized, "unauthorized", "the launch link was already used or has expired; run `theo ui` again")
	}
	http.SetCookie(w, sessionCookie(sess))
	return writeJSON(w, http.StatusOK, map[string]string{"csrf_token": sess.csrf, "api_version": APIVersion})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sess *session) error {
	return writeJSON(w, http.StatusOK, map[string]string{"csrf_token": sess.csrf, "api_version": APIVersion})
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, map[string]any{"url": s.LaunchURL(), "port": s.port, "pid": os.Getpid()})
}

func (s *Server) handleFocus(w http.ResponseWriter, r *http.Request, sess *session) error {
	var req struct {
		Visible bool `json:"visible"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	s.auth.setVisible(sess, req.Visible)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- system ---

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, s.metaNow(r.Context()))
}

// metaNow is the answer of GET /meta, cached for 10 s.
func (s *Server) metaNow(parent context.Context) Meta {
	s.meta.mu.Lock()
	defer s.meta.mu.Unlock()
	if time.Since(s.meta.at) > 10*time.Second {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		m := Meta{TheoVersion: engine.Version, APIVersion: APIVersion, OS: runtime.GOOS, Arch: runtime.GOARCH, DataDir: s.cfg.DataDir}
		m.Docker = toolVersion(ctx, "docker", "version", "--format", "{{.Server.Version}}")
		m.Git = toolVersion(ctx, "git", "--version")
		m.Git.Version = strings.TrimPrefix(m.Git.Version, "git version ")
		if m.Docker.Available {
			m.Compose = toolVersion(ctx, "docker", "compose", "version", "--short")
		} else {
			m.Compose = ToolInfo{Error: "Docker is not available"}
		}
		m.Compose.MinVersion, m.Git.MinVersion = composeMin(), gitMin()
		s.meta.data, s.meta.at = m, time.Now()
	}
	m := s.meta.data
	m.Idle = IdleInfo{TimeoutSeconds: int(s.cfg.IdleTimeout / time.Second)}
	return m
}

func toolVersion(ctx context.Context, name string, args ...string) ToolInfo {
	out, err := execx.Run(ctx, "", name, args...)
	if err != nil {
		return ToolInfo{Error: firstLine(err.Error())}
	}
	return ToolInfo{Available: true, Version: strings.TrimSpace(string(out))}
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, s.eng.Doctor(r.Context()))
}

// --- projects ---

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, _ *session) error {
	list, err := s.eng.Projects()
	if err != nil {
		return err
	}
	if list == nil {
		list = nil
	}
	return writeList(w, list, len(list))
}

// writeList writes a slice as JSON, an empty one as [] and not null.
func writeList(w http.ResponseWriter, v any, n int) error {
	if n == 0 {
		return writeJSON(w, http.StatusOK, []any{})
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Path    string `json:"path"`
		Name    string `json:"name"`
		EnvFile string `json:"env_file"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Path) == "" {
		return badRequest("path is required")
	}
	p, err := s.eng.AddProject(r.Context(), req.Path, req.Name, req.EnvFile)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request, _ *session) error {
	name := r.PathValue("p")
	var req map[string]json.RawMessage
	if err := decode(r, &req); err != nil {
		return err
	}
	if _, err := s.eng.Project(name); err != nil {
		return projectNotFound(name)
	}
	str := func(key string) (string, bool, error) {
		raw, ok := req[key]
		if !ok {
			return "", false, nil
		}
		if string(raw) == "null" {
			return "", true, nil
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", false, badRequest(key + " must be a string or null")
		}
		return v, true, nil
	}
	for k := range req {
		if k != "env_file" && k != "worktree_dir" {
			return errf(http.StatusBadRequest, "unknown_field", "unknown field "+quote(k))
		}
	}
	envFile, hasEnv, err := str("env_file")
	if err != nil {
		return err
	}
	dir, hasDir, err := str("worktree_dir")
	if err != nil {
		return err
	}
	if hasEnv {
		if _, err := s.eng.SetEnvFile(name, envFile); err != nil {
			return err
		}
	}
	if hasDir {
		if err := s.eng.SetWorktreeDir(name, dir); err != nil {
			return err
		}
	}
	p, err := s.eng.Project(name)
	if err != nil {
		return err
	}
	s.cache.invalidate(name)
	return writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleRemoveProject(w http.ResponseWriter, r *http.Request, _ *session) error {
	name := r.PathValue("p")
	if _, err := s.eng.Project(name); err != nil {
		return projectNotFound(name)
	}
	forget := r.URL.Query().Get("forget_records") == "1"
	res, err := s.eng.RemoveProjectChecked(r.Context(), name, engine.RemoveProjectOptions{ForgetRecords: forget})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: name})
	}
	s.cache.invalidate(name)
	return writeJSON(w, http.StatusOK, map[string]any{"removed": true, "files_kept": true, "containers_kept": true, "records_dropped": forget, "resources": res})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, _ *session) error {
	ov, err := s.overview(r.Context(), r.PathValue("p"), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ov)
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request, _ *session) error {
	name := r.PathValue("p")
	if _, err := s.eng.Project(name); err != nil {
		return projectNotFound(name)
	}
	list, err := s.eng.Branches(r.Context(), name)
	if err != nil {
		return err
	}
	return writeList(w, list, len(list))
}

// --- worktrees ---

func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request, _ *session) error {
	ov, err := s.overview(r.Context(), r.PathValue("p"), false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ov.Worktrees)
}

// instance resolves the worktree of the request and its instance.
func (s *Server) instance(r *http.Request) (*engine.Instance, engine.Worktree, error) {
	project := r.PathValue("p")
	wt, err := s.resolveWorktree(r.Context(), project, r.PathValue("wt"))
	if err != nil {
		return nil, wt, err
	}
	inst, err := s.eng.InstanceFor(r.Context(), project, wt.Path, engine.InstanceOptions{})
	if err != nil {
		return nil, wt, fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: r.PathValue("wt")})
	}
	return inst, wt, nil
}

func (s *Server) handleWorktree(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	wt, err := s.resolveWorktree(r.Context(), project, r.PathValue("wt"))
	if err != nil {
		return err
	}
	v := WorktreeView{ID: worktreeID(wt.Path), Path: wt.Path, Branch: wt.Branch, Head: wt.Head, Detached: wt.Detached, Locked: wt.Locked,
		Prunable: wt.Prunable, Bare: wt.Bare, Main: wt.Kind == engine.KindMain, Kind: wt.Kind, Owner: wt.Owner, Dirty: wt.Dirty,
		State: engine.InstUnknown, Ports: []engine.PortStatus{}, Warnings: []engine.StatusWarning{}}
	out := struct {
		WorktreeView
		Settings   *SettingsView       `json:"settings,omitempty"`
		Containers []compose.Container `json:"containers"`
		Services   []string            `json:"expected_services,omitempty"`
		Counts     ContainerCount      `json:"container_counts"`
	}{WorktreeView: v, Containers: []compose.Container{}}
	if !wt.Bare && !wt.Prunable {
		s.fillInstance(r.Context(), project, wt, &out.WorktreeView, func(error) {})
		if inst, err := s.eng.InstanceFor(r.Context(), project, wt.Path, engine.InstanceOptions{}); err == nil {
			out.Settings = s.settingsView(inst)
			if cs, err := s.eng.Containers(r.Context(), inst); err == nil && cs != nil {
				out.Containers = cs
			}
			out.Services = s.eng.ExpectedServices(inst)
		}
	}
	out.Counts = out.WorktreeView.Containers
	out.Busy = s.busyRef(project, out.ID)
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	var req struct {
		Branch string `json:"branch"`
		New    bool   `json:"new"`
		From   string `json:"from"`
		Path   string `json:"path"`
		Copy   bool   `json:"copy"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if _, err := s.eng.Project(project); err != nil {
		return projectNotFound(project)
	}
	if strings.TrimSpace(req.Branch) == "" {
		return badRequest("branch is required")
	}
	// Git would read these as options (--force, --detach...).
	if strings.HasPrefix(req.Branch, "-") || strings.HasPrefix(req.From, "-") {
		return badRequest("branch and from must not start with \"-\"")
	}
	added, err := s.eng.AddWorktree(r.Context(), project, req.Branch, engine.AddWorktreeOptions{NewBranch: req.New, Base: req.From, Path: req.Path, Copy: req.Copy})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: project})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	notes := append([]string{}, added.Notes...)
	if inst, err := s.eng.InstanceFor(r.Context(), project, added.Path, engine.InstanceOptions{}); err == nil {
		if names := inst.FixedContainerNames(); len(names) > 0 {
			notes = append(notes, fmt.Sprintf("%s set a fixed container_name, so this worktree cannot run at the same time as another one. Turn on \"Neutralize fixed container names\" in its Summary tab before starting it.",
				strings.Join(compose.SortedKeys(names), ", ")))
		}
	}
	return writeJSON(w, http.StatusCreated, struct {
		ID string `json:"id"`
		*engine.AddedWorktree
		Notes []string `json:"notes"`
	}{worktreeID(added.Path), added, notes})
}

func (s *Server) handleRemoveWorktree(w http.ResponseWriter, r *http.Request, _ *session) error {
	project, id := r.PathValue("p"), r.PathValue("wt")
	var req struct {
		DiscardChanges bool   `json:"discard_changes"`
		Confirm        string `json:"confirm"`
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
	// The server asks the engine every time; there is no generic confirm
	// and no force. The engine decides whether the text is right.
	res, err := s.eng.RemoveWorkspace(r.Context(), project, wt.Path, engine.RemoveWorktreeOptions{
		DiscardChanges: req.DiscardChanges || req.Confirm != "", Confirm: req.Confirm})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: id})
	}
	s.cache.invalidate(project)
	s.hub.publish("state.changed", map[string]string{"project": project})
	return writeJSON(w, http.StatusOK, map[string]any{"removed": true, "path": res.Worktree.Path, "branch": res.Worktree.Branch, "kind": res.Kind,
		"preserved": []string{"the branch", "all volumes", "the other worktrees"}})
}

// --- lifecycle operations ---

type upRequest struct {
	Build       bool     `json:"build"`
	Profiles    []string `json:"profiles"`
	AutoPorts   bool     `json:"auto_ports"`
	EnvFiles    []string `json:"env_files"`
	StartShared bool     `json:"start_shared"`
}

func (s *Server) lifecycle(kind string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, sess *session) error {
		project, id := r.PathValue("p"), r.PathValue("wt")
		var req upRequest
		if err := decodeOptional(r, &req); err != nil {
			return err
		}
		if kind != "up" && (req.Build || len(req.Profiles) > 0 || req.AutoPorts || len(req.EnvFiles) > 0) {
			return badRequest("options are only valid for up")
		}
		wt, err := s.resolveWorktree(r.Context(), project, id)
		if err != nil {
			return err
		}
		if wt.Bare || wt.Prunable {
			return errf(http.StatusConflict, "invalid_request", "this worktree has no directory to run an instance in")
		}
		opts := engine.InstanceOptions{EnvFiles: req.EnvFiles}
		run := func(ctx context.Context, stdout, stderr io.Writer) (string, error) {
			inst, err := s.eng.InstanceFor(ctx, project, wt.Path, opts)
			if err != nil {
				return "", err
			}
			switch kind {
			case "up":
				return "", s.runUp(ctx, inst, req, stdout, stderr)
			case "down":
				if err := s.eng.Down(ctx, inst, stdout, stderr); err != nil {
					return "", err
				}
				return "Stopped " + inst.Compose + ". Its volumes were kept.", nil
			default:
				if err := s.eng.Restart(ctx, inst, stdout, stderr); err != nil {
					return "", err
				}
				return "Restarted " + inst.Compose + ".", nil
			}
		}
		op, replay, err := s.ops.start(startRequest{kind: kind, project: project, worktree: id, key: instanceKey(project, id),
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

// runUp does what `theo up` does, in the same order, writing the notes that
// the CLI prints on stderr into the operation's log.
func (s *Server) runUp(ctx context.Context, inst *engine.Instance, req upRequest, stdout, stderr io.Writer) error {
	e := s.eng
	if req.AutoPorts {
		if err := e.SetAutoPorts(inst, true); err != nil {
			return err
		}
	}
	warnings, err := e.EnsureShared(ctx, inst, req.StartShared, stdout, stderr)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "theo: warning: %s\n", w)
	}
	plan, err := e.ApplyInstance(ctx, inst, req.Profiles, req.AutoPorts)
	if err != nil {
		return err
	}
	for _, m := range plan.Mappings {
		if m.Status == engine.PortReassigned || m.Status == engine.PortKept {
			fmt.Fprintf(stderr, "theo: %s publishes %d/%s on host port %d instead of %s (%s)\n", m.Service, m.Target, m.Protocol, m.Effective, m.Configured, m.Status)
		}
	}
	for _, m := range plan.Conflicts() {
		fmt.Fprintf(stderr, "theo: warning: host port %s (%s, container port %d/%s) is already in use\n", m.Configured, m.Service, m.Target, m.Protocol)
	}
	if err := e.Up(ctx, inst, compose.UpOptions{Build: req.Build, Profiles: req.Profiles}, stdout, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Started %s.\n", inst.Compose)
	return nil
}

// --- read-only instance views ---

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request, _ *session) error {
	wt, err := s.resolveWorktree(r.Context(), r.PathValue("p"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	svc, err := s.eng.Services(r.Context(), r.PathValue("p"), wt.Path, nil)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, svc)
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	plan, err := s.eng.PlanPorts(r.Context(), inst, q["profile"], q.Get("auto_ports") == "1")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, struct {
		*engine.Instance
		*engine.PortPlan
	}{inst, plan})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	text, err := s.eng.PreviewConfig(r.Context(), inst, q["profile"], q.Get("auto_ports") == "1") // a GET reserves nothing
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = io.WriteString(w, text)
	return err
}

// EnvVar is a variable of an instance as the API shows it.
type EnvVar struct {
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Secret   bool     `json:"secret"`
	Services []string `json:"services"`
}

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	cfg, err := s.eng.Settings(inst)
	if err != nil {
		return err
	}
	out := []EnvVar{}
	names := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := cfg.Env[k]
		if v.Secret {
			v.Value = engine.Mask // secrets never leave through a list
		}
		svcs := v.Services
		if svcs == nil {
			svcs = []string{}
		}
		out = append(out, EnvVar{Name: k, Value: v.Value, Secret: v.Secret, Services: svcs})
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetEnv(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Value    string   `json:"value"`
		Secret   bool     `json:"secret"`
		Services []string `json:"services"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := s.eng.SetEnv(inst, name, req.Value, req.Secret, req.Services); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"name": name, "applies_on": "next_up"})
}

func (s *Server) handleUnsetEnv(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	found, err := s.eng.UnsetEnv(inst, name)
	if err != nil {
		return err
	}
	if !found {
		return &Error{Status: http.StatusNotFound, Code: "env_not_found", Message: inst.Compose + " has no variable " + name + " set with theo"}
	}
	return writeJSON(w, http.StatusOK, map[string]string{"name": name, "applies_on": "next_up"})
}

// handleReveal returns the value of ONE variable. The response is never
// cached and nothing in the server logs bodies.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	cfg, err := s.eng.Settings(inst)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	v, ok := cfg.Env[name]
	if !ok {
		return &Error{Status: http.StatusNotFound, Code: "env_not_found", Message: inst.Compose + " has no variable " + name + " set with theo"}
	}
	w.Header().Set("Cache-Control", "no-store")
	return writeJSON(w, http.StatusOK, map[string]any{"name": name, "value": v.Value, "secret": v.Secret})
}

// SettingsView is what Theo keeps for an instance, without variables.
type SettingsView struct {
	NeutralizeContainerNames bool              `json:"neutralize_container_names"`
	AutoPorts                bool              `json:"auto_ports"`
	Profiles                 []string          `json:"profiles"`
	Shared                   []string          `json:"shared"`
	Join                     []string          `json:"join"`
	FixedContainerNames      map[string]string `json:"fixed_container_names"`
}

func (s *Server) settingsView(inst *engine.Instance) *SettingsView {
	cfg, err := s.eng.Settings(inst)
	if err != nil {
		return nil
	}
	nz := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	names := inst.FixedContainerNames()
	if names == nil {
		names = map[string]string{}
	}
	return &SettingsView{NeutralizeContainerNames: cfg.NeutralizeContainerNames, AutoPorts: cfg.AutoPorts, Profiles: nz(inst.Profiles),
		Shared: nz(cfg.Shared), Join: nz(cfg.Join), FixedContainerNames: names}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.settingsView(inst))
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Neutralize *bool `json:"neutralize_container_names"`
		AutoPorts  *bool `json:"auto_ports"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	if req.Neutralize != nil {
		if err := s.eng.SetNeutralizeContainerNames(inst, *req.Neutralize); err != nil {
			return err
		}
	}
	if req.AutoPorts != nil {
		if err := s.eng.SetAutoPorts(inst, *req.AutoPorts); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, s.settingsView(inst))
}

func (s *Server) handleVolumes(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	list, err := s.eng.Volumes(r.Context(), inst)
	if err != nil {
		return err
	}
	return writeList(w, list, len(list))
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	project, id := r.PathValue("p"), r.PathValue("wt")
	if op := s.ops.busy(instanceKey(project, id)); op != nil {
		e := errf(http.StatusConflict, "busy", "an operation is running on this instance")
		e.Operation = &OperationRef{ID: op.ID, Kind: op.Kind}
		return e
	}
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	removed, err := s.eng.PurgeVolumes(r.Context(), inst, req.Confirm)
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: id})
	}
	if removed == nil {
		removed = []engine.VolumeInfo{}
	}
	s.cache.invalidate(project)
	return writeJSON(w, http.StatusOK, struct {
		Compose string              `json:"compose_project"`
		Deleted []engine.VolumeInfo `json:"deleted"`
	}{inst.Compose, removed})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, _ *session) error {
	wt, err := s.resolveWorktree(r.Context(), r.PathValue("p"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	limit, before, err := historyParams(r)
	if err != nil {
		return err
	}
	page, err := s.eng.HistoryPage(r.Context(), r.PathValue("p"), wt.Path, limit, before)
	if err != nil {
		return err
	}
	return writeHistory(w, page)
}

// --- directory browser ---

// maxBrowse bounds the entries returned for one directory.
const maxBrowse = 1000

// BrowseEntry is a directory.
type BrowseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Repo bool   `json:"is_repo"`
}

// handleBrowse lists the sub-directories of a directory, so that the page
// can offer a folder picker (Q-027). It returns directory names only: never
// file names or contents. The default is the user's home directory; any
// absolute path can be asked for explicitly (the user is already allowed to
// read it); hidden directories are skipped unless hidden=1.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request, _ *session) error {
	dir := r.URL.Query().Get("path")
	home, _ := os.UserHomeDir()
	if dir == "" {
		dir = home
	}
	if networkPath(dir, runtime.GOOS) {
		return badRequest("network and device paths (\\\\server\\share, \\\\.\\...) are not browsed: Windows would connect to that host with your credentials")
	}
	if !filepath.IsAbs(dir) {
		return badRequest("path must be absolute")
	}
	dir = filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "not a directory that can be read"}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "this directory cannot be read"}
	}
	showHidden := r.URL.Query().Get("hidden") == "1"
	out := []BrowseEntry{}
	truncated := false
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		if !e.IsDir() { // a symbolic link to a directory counts as one; anything else is skipped without a stat
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
				continue
			}
		}
		if len(out) >= maxBrowse {
			truncated = true
			break
		}
		_, gerr := os.Stat(filepath.Join(full, ".git"))
		out = append(out, BrowseEntry{Name: name, Path: full, Repo: gerr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	_, isRepo := os.Stat(filepath.Join(dir, ".git"))
	return writeJSON(w, http.StatusOK, map[string]any{"path": dir, "parent": parent, "home": home, "is_repo": isRepo == nil,
		"entries": out, "truncated": truncated})
}
