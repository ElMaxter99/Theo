package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/mcp"
)

// Routes added by the 0.2 redesign of the interface (docs/api.md, «0.2
// additions»): a global status and history for the home screen, stop-all,
// renaming a project, pruning merged worktrees, volume backups, restores and
// clones, and the MCP registration for the AI agents settings.
func (s *Server) redesignRoutes() {
	s.handle("GET", "/status", authSession, s.handleStatus)
	s.handle("GET", "/history", authSession, s.handleAllHistory)
	s.handle("POST", "/stop-all", authSession, s.handleStopAll)
	s.handle("GET", "/mcp/install", authSession, s.handleMCPInstall)

	s.handle("POST", "/projects/{p}/rename", authSession, s.handleRenameProject)
	s.handle("GET", "/projects/{p}/prune", authSession, s.handlePrune(false))
	s.handle("POST", "/projects/{p}/prune", authSession, s.handlePrune(true))

	s.handle("GET", "/projects/{p}/worktrees/{wt}/volumes/backups", authSession, s.handleBackups)
	s.handle("POST", "/projects/{p}/worktrees/{wt}/volumes/backup", authSession, s.volumeOperation("backup"))
	s.handle("POST", "/projects/{p}/worktrees/{wt}/volumes/restore", authSession, s.volumeOperation("restore"))
	s.handle("POST", "/projects/{p}/worktrees/{wt}/volumes/clone", authSession, s.volumeOperation("clone"))
}

// handleStatus is `theo status --json`: every project, worktree and instance.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, _ *session) error {
	report, err := s.eng.Status(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		return err
	}
	// Each worktree also gets the id the other routes take.
	type worktree struct {
		ID string `json:"id"`
		engine.WorktreeStatus
	}
	type project struct {
		engine.ProjectStatus
		Worktrees []worktree `json:"worktrees"`
	}
	out := struct {
		*engine.StatusReport
		Projects []project `json:"projects"`
	}{StatusReport: report, Projects: []project{}}
	for _, p := range report.Projects {
		v := project{ProjectStatus: p, Worktrees: []worktree{}}
		for _, wt := range p.Worktrees {
			v.Worktrees = append(v.Worktrees, worktree{ID: worktreeID(wt.Path), WorktreeStatus: wt})
		}
		out.Projects = append(out.Projects, v)
	}
	return writeJSON(w, http.StatusOK, out)
}

// handleAllHistory is the history of every project, newest page first, with
// the same paging headers as the history of one project.
func (s *Server) handleAllHistory(w http.ResponseWriter, r *http.Request, _ *session) error {
	limit, before, err := historyParams(r)
	if err != nil {
		return err
	}
	page, err := s.eng.AllHistory(limit, before)
	if err != nil {
		return err
	}
	return writeHistory(w, page)
}

// stopAllKey is the key of the stop-all operation: one at a time.
const stopAllKey = "stop-all"

// handleStopAll stops every running instance, of every project or of one,
// like `theo stop --all [project]`. Containers and volumes are kept.
func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request, sess *session) error {
	var req struct {
		Project string `json:"project"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	names := []string{}
	if req.Project != "" {
		p, err := s.eng.Project(req.Project)
		if err != nil {
			return projectNotFound(req.Project)
		}
		names = append(names, p.Name)
	} else {
		list, err := s.eng.Projects()
		if err != nil {
			return err
		}
		for _, p := range list {
			names = append(names, p.Name)
		}
	}
	run := func(ctx context.Context, stdout, stderr io.Writer) (string, error) {
		var stopped, failed []string
		for _, name := range names {
			overview, err := s.eng.Overview(ctx, name)
			if err != nil {
				failed = append(failed, name+": "+firstLine(err.Error()))
				continue
			}
			for _, o := range overview {
				if !stacksRunning(o.Stacks) {
					continue
				}
				inst, err := s.eng.Instance(ctx, name, o.Worktree.Path, nil)
				if err != nil {
					failed = append(failed, name+" "+o.Worktree.Path+": "+firstLine(err.Error()))
					continue
				}
				fmt.Fprintf(stderr, "theo: stopping %s\n", inst.Compose)
				if err := s.eng.Stop(ctx, inst, nil, stdout, stderr); err != nil {
					failed = append(failed, inst.Compose+": "+firstLine(err.Error()))
					continue
				}
				stopped = append(stopped, inst.Compose)
			}
			s.cache.invalidate(name)
		}
		if len(failed) > 0 {
			return "", fmt.Errorf("could not stop %d instance(s): %s", len(failed), strings.Join(failed, "; "))
		}
		if len(stopped) == 0 {
			return "No instance was running.", nil
		}
		return fmt.Sprintf("Stopped %d instance(s): %s. Their containers and volumes were kept.", len(stopped), strings.Join(stopped, ", ")), nil
	}
	op, _, err := s.ops.start(startRequest{kind: "stop-all", project: req.Project, key: stopAllKey,
		session: sess.id, idemKey: r.Header.Get("Idempotency-Key"), run: run})
	if err != nil {
		return err
	}
	w.Header().Set("Location", apiBase+"/operations/"+op.ID)
	return writeJSON(w, http.StatusAccepted, op.view())
}

func stacksRunning(stacks []compose.Stack) bool {
	for _, st := range stacks {
		if strings.Contains(st.Status, "running") {
			return true
		}
	}
	return false
}

// handleMCPInstall is `theo mcp install --json`: how to register Theo's MCP
// server with an AI client, and the paragraph for AGENTS.md.
func (s *Server) handleMCPInstall(w http.ResponseWriter, r *http.Request, _ *session) error {
	q := r.URL.Query()
	owner := q.Get("owner")
	if owner != "" {
		var err error
		if owner, err = engine.ValidateOwner(owner); err != nil {
			return badRequest("owner: " + err.Error())
		}
	}
	scope := q.Get("scope")
	switch scope {
	case "", "local", "user", "project":
	default:
		return badRequest("scope must be local, user or project")
	}
	doc := mcp.InstallDoc(mcp.TheoCommand(), "theo", owner, scope, q.Get("read_only") == "1")
	return writeJSON(w, http.StatusOK, struct {
		mcp.Install
		ClaudeCodeCommand string `json:"claude_code_command"`
	}{doc, mcp.ShellJoin(doc.ClaudeCode)})
}

// handleRenameProject renames a project, like `theo project rename`.
func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request, _ *session) error {
	name := r.PathValue("p")
	var req struct {
		Name string `json:"name"`
		Yes  bool   `json:"yes"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if _, err := s.eng.Project(name); err != nil {
		return projectNotFound(name)
	}
	if strings.TrimSpace(req.Name) == "" {
		return badRequest("name is required")
	}
	if op := s.ops.busyProject(name); op != nil {
		e := errf(http.StatusConflict, "busy", "an operation is running on this project")
		e.Operation = &OperationRef{ID: op.ID, Kind: op.Kind}
		return e
	}
	res, err := s.eng.RenameProject(r.Context(), name, req.Name, engine.RenameOptions{Yes: req.Yes})
	if err != nil {
		return fromEngine(err, &ResourceRef{Kind: "project", Project: name})
	}
	s.cache.invalidateAll()
	s.hub.publish("state.changed", map[string]string{"project": res.New})
	return writeJSON(w, http.StatusOK, res)
}

// handlePrune lists (GET) or removes (POST) the linked worktrees whose branch
// is merged, like `theo worktree prune`. POST needs the project name typed in confirm.
func (s *Server) handlePrune(apply bool) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, _ *session) error {
		name := r.PathValue("p")
		var req struct {
			Into    string `json:"into"`
			Confirm string `json:"confirm"`
		}
		if apply {
			if err := decodeOptional(r, &req); err != nil {
				return err
			}
		} else {
			req.Into = r.URL.Query().Get("into")
		}
		p, err := s.eng.Project(name)
		if err != nil {
			return projectNotFound(name)
		}
		if strings.HasPrefix(req.Into, "-") {
			return badRequest("into must not start with \"-\"")
		}
		if apply {
			if req.Confirm != p.Name {
				e := errf(http.StatusConflict, "confirmation_required", "type the project name "+quote(p.Name)+" to remove its merged worktrees")
				e.Hint = "GET the same path first to see which worktrees would go"
				return e
			}
			if op := s.ops.busyProject(name); op != nil {
				e := errf(http.StatusConflict, "busy", "an operation is running on this project")
				e.Operation = &OperationRef{ID: op.ID, Kind: op.Kind}
				return e
			}
		}
		report, err := s.eng.PruneMerged(r.Context(), p.Name, engine.PruneOptions{Into: req.Into, Apply: apply})
		if err != nil {
			return fromEngine(err, &ResourceRef{Kind: "project", Project: name})
		}
		if report.Merged == nil {
			report.Merged = []engine.PruneEntry{}
		}
		if apply {
			s.cache.invalidate(p.Name)
			s.hub.publish("state.changed", map[string]string{"project": p.Name})
		}
		return writeJSON(w, http.StatusOK, report)
	}
}

// handleBackups lists the backups of the instance kept in Theo's data
// directory, newest first.
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	list, err := s.eng.Backups(inst.Project, inst.Compose)
	if err != nil {
		return err
	}
	return writeList(w, list, len(list))
}

// volumeRequest is the body of the volume operations.
type volumeRequest struct {
	Volumes []string `json:"volumes"` // backup and clone: Compose names; all when empty
	// Backup is the archive file name of a backup listed by
	// GET …/volumes/backups (restore). Paths are refused: only Theo's own
	// backups of this project can be restored through the API.
	Backup string `json:"backup"`
	Volume string `json:"volume"` // restore: the target volume; from the manifest when empty
	// From is the id of the worktree whose volumes are copied (clone).
	From    string `json:"from"`
	Confirm string `json:"confirm"` // restore and clone: the Compose project name of this instance
	Replace bool   `json:"replace"` // restore and clone: overwrite volumes that have data
}

// volumeOperation runs a backup, a restore or a clone as an operation on the
// instance. Restore and clone check the typed confirmation before starting.
func (s *Server) volumeOperation(kind string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, sess *session) error {
		project, id := r.PathValue("p"), r.PathValue("wt")
		var req volumeRequest
		if err := decodeOptional(r, &req); err != nil {
			return err
		}
		inst, _, err := s.instance(r)
		if err != nil {
			return err
		}
		var src *engine.Instance
		archive := ""
		switch kind {
		case "restore":
			if req.Backup == "" || filepath.Base(req.Backup) != req.Backup || !strings.HasSuffix(req.Backup, ".tar.gz") {
				return badRequest("backup must be the file name of a backup listed by GET volumes/backups")
			}
			dir, err := s.eng.BackupDir(inst.Project)
			if err != nil {
				return err
			}
			archive = filepath.Join(dir, req.Backup)
		case "clone":
			if req.From == "" || req.From == id {
				return badRequest("from must be the id of another worktree of the project")
			}
			wt, err := s.resolveWorktree(r.Context(), project, req.From)
			if err != nil {
				return err
			}
			if src, err = s.eng.InstanceFor(r.Context(), project, wt.Path, engine.InstanceOptions{}); err != nil {
				return fromEngine(err, &ResourceRef{Kind: "instance", Project: project, Worktree: req.From})
			}
		}
		if kind != "backup" && req.Confirm != inst.Compose {
			e := errf(http.StatusConflict, "confirmation_required", "type the instance name "+quote(inst.Compose)+" to overwrite its data")
			e.Resource = &ResourceRef{Kind: "instance", Project: project, Worktree: id}
			return e
		}
		run := func(ctx context.Context, stdout, stderr io.Writer) (string, error) {
			switch kind {
			case "backup":
				report, err := s.eng.BackupVolumes(ctx, inst, req.Volumes, "")
				if report != nil {
					for _, w := range report.Warnings {
						fmt.Fprintf(stderr, "theo: warning: %s\n", w)
					}
					for _, b := range report.Backups {
						fmt.Fprintf(stdout, "%s → %s (%d bytes)\n", b.Docker, b.File, b.Size)
					}
				}
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Backed up %d volume(s) of %s.", len(report.Backups), inst.Compose), nil
			case "restore":
				res, err := s.eng.RestoreVolume(ctx, inst, engine.RestoreOptions{From: archive, Volume: req.Volume, Confirm: req.Confirm, Replace: req.Replace})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Restored %s from %s.", res.Docker, filepath.Base(res.From)), nil
			default:
				report, err := s.eng.CloneVolumes(ctx, src, inst, engine.CloneOptions{Volumes: req.Volumes, Confirm: req.Confirm, Replace: req.Replace})
				if err != nil {
					return "", err
				}
				for _, c := range report.Volumes {
					fmt.Fprintf(stdout, "%s → %s\n", c.From, c.To)
				}
				return fmt.Sprintf("Copied %d volume(s) from %s into %s.", len(report.Volumes), src.Compose, inst.Compose), nil
			}
		}
		op, replay, err := s.ops.start(startRequest{kind: "volumes." + kind, project: project, worktree: id, key: instanceKey(project, id),
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
