package api

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/instances"
)

// Reconcile, health, diagnostics and paginated history (docs/api.md,
// sections "Reconcile and health" and "Diagnostics").

func (s *Server) reconcileRoutes() {
	s.handle("GET", "/reconcile", authSession, s.handleReconcile)
	s.handle("POST", "/reconcile/apply", authSession, s.handleReconcileApply)
	s.handle("GET", "/health", authSession, s.handleHealth)
	s.handle("GET", "/diagnostics", authSession, s.handleDiagnostics)
	s.handle("GET", "/projects/{p}/history", authSession, s.handleProjectHistory)
}

// reconcileReport is the CLI's `reconcile --json`: the report and how many
// records were forgotten (0 unless applying).
type reconcileReport struct {
	*engine.Report
	Forgotten int `json:"forgotten"`
}

func (s *Server) reconcile(r *http.Request, project string) (*engine.Report, error) {
	if project != "" {
		if _, err := s.eng.Project(project); err != nil {
			return nil, projectNotFound(project)
		}
	}
	return s.eng.Reconcile(r.Context(), project)
}

// reconcileShared is reconcile for the read-only GET: concurrent requests for
// the same scope share one computation, which runs with bounded commands and
// timeouts and does not stop when one client leaves.
func (s *Server) reconcileShared(r *http.Request, project string) (*engine.Report, error) {
	if project != "" {
		if _, err := s.eng.Project(project); err != nil {
			return nil, projectNotFound(project)
		}
	}
	rep, _, err := s.repFlight.do(r.Context(), strings.ToLower(project), s.cache.generation(), func() (*engine.Report, error) {
		ctx, cancel := s.work()
		defer cancel()
		return s.eng.Reconcile(ctx, project)
	})
	return rep, err
}

// handleReconcile serves GET /reconcile[?project=]: the same JSON as
// `theo reconcile --json`. It changes nothing.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request, _ *session) error {
	rep, err := s.reconcileShared(r, r.URL.Query().Get("project"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, reconcileReport{rep, 0})
}

// handleReconcileApply serves POST /reconcile/apply. The server computes the
// report again and forgets only the findings that are Theo's own records
// (`forgettable`) and whose ids the client sent, or all of them with
// {"all": true}. Containers, volumes, worktrees and files of the user are
// never touched. The answer has the shape of `theo reconcile --apply --json`
// (the findings are the ones found before forgetting), plus `applied` and
// `skipped`.
func (s *Server) handleReconcileApply(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Project string   `json:"project"`
		IDs     []string `json:"ids"`
		All     bool     `json:"all"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !req.All && len(req.IDs) == 0 {
		return badRequest("say which findings to forget: ids, or all: true")
	}
	if req.All && len(req.IDs) > 0 {
		return badRequest("ids and all are exclusive")
	}
	rep, err := s.reconcile(r, req.Project)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range req.IDs {
		want[id] = true
	}
	pick := &engine.Report{Findings: []engine.Finding{}, DockerError: rep.DockerError}
	applied, skipped := []string{}, []string{}
	for _, f := range rep.Findings {
		if !f.Forgettable || !(req.All || want[f.ID]) {
			continue
		}
		pick.Findings = append(pick.Findings, f)
		applied = append(applied, f.ID)
		delete(want, f.ID)
	}
	for _, id := range req.IDs {
		if want[id] { // not found, or not Theo's own record
			skipped = append(skipped, id)
			delete(want, id)
		}
	}
	n, err := s.eng.ApplyReconcile(pick)
	if err != nil {
		return err
	}
	s.cache.invalidateAll()
	s.health.invalidate()
	s.hub.publish("state.changed", map[string]string{})
	return writeJSON(w, http.StatusOK, struct {
		reconcileReport
		Applied []string `json:"applied"`
		Skipped []string `json:"skipped"`
	}{reconcileReport{rep, n}, applied, skipped})
}

// --- health ---

// Health is the answer of GET /health: what the indicator of the shell
// shows. It is derived from the reconcile report of all projects.
type Health struct {
	// Status is "ok", "attention" (findings) or "docker_unavailable".
	Status      string `json:"status"`
	Findings    int    `json:"findings"`
	Forgettable int    `json:"forgettable"`
	DockerError string `json:"docker_error,omitempty"`
}

type healthCache struct {
	mu   sync.Mutex
	at   time.Time
	gen  uint64
	data Health
	ok   bool
}

func (h *healthCache) invalidate() { h.mu.Lock(); h.ok = false; h.mu.Unlock() }

// healthTTL is how long an answer of GET /health is reused.
const healthTTL = 5 * time.Second

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, _ *session) error {
	gen := s.cache.generation()
	s.health.mu.Lock()
	if s.health.ok && s.health.gen == gen && time.Since(s.health.at) < healthTTL {
		h := s.health.data
		s.health.mu.Unlock()
		return writeJSON(w, http.StatusOK, h)
	}
	s.health.mu.Unlock()
	h, _, err := s.healthFlight.do(r.Context(), "health", gen, func() (Health, error) {
		ctx, cancel := s.work()
		defer cancel()
		rep, err := s.eng.Reconcile(ctx, "")
		if err != nil {
			return Health{}, err
		}
		h := Health{Status: "ok", Findings: len(rep.Findings), DockerError: rep.DockerError}
		for _, f := range rep.Findings {
			if f.Forgettable {
				h.Forgettable++
			}
		}
		switch {
		case rep.DockerError != "":
			h.Status = "docker_unavailable"
		case h.Findings > 0:
			h.Status = "attention"
		}
		s.health.mu.Lock()
		if s.cache.generation() == gen {
			s.health.data, s.health.at, s.health.gen, s.health.ok = h, time.Now(), gen, true
		}
		s.health.mu.Unlock()
		return h, nil
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, h)
}

// --- diagnostics ---

// Diagnostics is the answer of GET /diagnostics: everything the settings
// screen shows, and a plain-text report that is safe to paste into an issue
// (no environment variables, no secrets, no project file contents).
type Diagnostics struct {
	Meta   Meta                `json:"meta"`
	Doctor engine.DoctorReport `json:"doctor"`
	// Projects is how many projects are registered.
	Projects int    `json:"projects"`
	Report   string `json:"report"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request, _ *session) error {
	meta := s.metaNow(r.Context())
	doc := s.eng.Doctor(r.Context())
	projects, err := s.eng.Projects()
	if err != nil {
		return err
	}
	d := Diagnostics{Meta: meta, Doctor: doc, Projects: len(projects)}
	var b strings.Builder
	// The data directory is the one path the report shows, on purpose (docs/api.md: the page says so before
	// copying). Every other path, in the details of the checks and in tool errors, is replaced.
	fmt.Fprintf(&b, "Theo %s (API %s)\nPlatform: %s/%s\nData directory: %s\nProjects: %d\n", meta.TheoVersion, meta.APIVersion, runtime.GOOS, runtime.GOARCH, meta.DataDir, len(projects))
	for _, t := range []struct {
		name string
		info ToolInfo
	}{{"Docker", meta.Docker}, {"Docker Compose", meta.Compose}, {"Git", meta.Git}} {
		state := t.info.Version
		if !t.info.Available {
			state = "not available: " + redactPaths(t.info.Error)
		}
		min := ""
		if t.info.MinVersion != "" {
			min = " (Theo needs " + t.info.MinVersion + " or later)"
		}
		fmt.Fprintf(&b, "%s: %s%s\n", t.name, state, min)
	}
	b.WriteString("\nChecks:\n")
	for _, c := range doc.Checks {
		fmt.Fprintf(&b, "- [%s] %s", c.Status, c.Name)
		if c.Detail != "" {
			b.WriteString(": " + redactPaths(firstLine(c.Detail)))
		}
		b.WriteString("\n")
	}
	d.Report = b.String()
	return writeJSON(w, http.StatusOK, d)
}

// composeMin and gitMin are what Theo needs (NFR-041).
func composeMin() string { return compose.MinVersion.String() }
func gitMin() string     { return engine.MinGitVersion.String() }

// --- history ---

// historyPage reads limit and before from the query.
func historyParams(r *http.Request) (limit, before int, err error) {
	before = -1
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 0 {
			return 0, 0, badRequest("limit must be a positive number")
		}
	}
	if v := r.URL.Query().Get("before"); v != "" {
		if before, err = strconv.Atoi(v); err != nil || before < 0 {
			return 0, 0, badRequest("before must be the cursor of a previous page")
		}
	}
	return limit, before, nil
}

// writeHistory writes a page as the array `theo history --json` prints, and
// the paging in headers, so the body stays the same with or without paging:
// Theo-History-Total (all matching events), Theo-History-Next (the cursor to
// pass as before for the older page; absent on the oldest page).
func writeHistory(w http.ResponseWriter, page engine.HistoryPageResult) error {
	w.Header().Set("Theo-History-Total", strconv.Itoa(page.Total))
	if page.Next >= 0 {
		w.Header().Set("Theo-History-Next", strconv.Itoa(page.Next))
	}
	events := page.Events
	if events == nil {
		events = []instances.Event{}
	}
	return writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleProjectHistory(w http.ResponseWriter, r *http.Request, _ *session) error {
	limit, before, err := historyParams(r)
	if err != nil {
		return err
	}
	page, err := s.eng.HistoryPage(r.Context(), r.PathValue("p"), "", limit, before)
	if err != nil {
		return err
	}
	return writeHistory(w, page)
}
