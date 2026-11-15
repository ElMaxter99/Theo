package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func (s *Server) captureRoutes() {
	s.handle("GET", "/projects/{p}/capture", authSession, s.handleCapture)
	s.handle("POST", "/projects/{p}/capture/compare", authSession, s.handleCaptureCompare)
	s.handle("GET", "/projects/{p}/capture/runs/{op}", authSession, s.handleCaptureRun)
	s.handle("POST", "/capture/setup", authSession, s.handleCaptureSetup)
	s.handle("GET", "/projects/{p}/capture/reports/{name}/{file...}", authSession, s.handleCaptureFile)
}

type captureSetView struct {
	Label    string `json:"label"`
	Commit   string `json:"commit,omitempty"`
	Date     string `json:"date"`
	Mode     string `json:"mode"`
	Screens  int    `json:"screens"`
	Shots    int    `json:"shots"`
	Unstable int    `json:"unstable"`
}

type captureStateView struct {
	Engine  bool                   `json:"engine"`
	Node    string                 `json:"node,omitempty"`
	Chrome  string                 `json:"chromium,omitempty"`
	Problem string                 `json:"problem,omitempty"`
	Summary *capture.Summary       `json:"inventory,omitempty"`
	NoInv   bool                   `json:"no_inventory"`
	InvErr  string                 `json:"inventory_error,omitempty"`
	Sets    []captureSetView       `json:"sets"`
	Reports []engine.CaptureReport `json:"reports"`
}

// handleCapture is what the "Tools" page shows for a project: whether the
// engine is installed, what the inventory holds, the sets and the reports.
func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request, _ *session) error {
	project := r.PathValue("p")
	root, err := s.eng.CaptureRoot(project)
	if err != nil {
		return projectNotFound(project)
	}
	v := captureStateView{Sets: []captureSetView{}, Reports: []engine.CaptureReport{}}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if st, err := s.eng.CaptureEngine().Check(ctx); err != nil {
		v.Problem = err.Error()
	} else {
		v.Engine, v.Node, v.Chrome = true, st.Node, st.Chromium
	}
	switch inv, err := capture.LoadInventory(root); {
	case errors.Is(err, capture.ErrNoInventory):
		v.NoInv = true
	case err != nil:
		v.InvErr = err.Error()
	default:
		sm := inv.Summarize()
		v.Summary = &sm
	}
	if sets, err := s.eng.CaptureSets(project); err == nil {
		for _, st := range sets {
			sv := captureSetView{Label: st.Label, Commit: st.Commit, Date: st.Date, Mode: st.Mode, Screens: len(st.Screens), Shots: len(st.Shots)}
			for _, sh := range st.Shots {
				if sh.Status == capture.ShotUnstable {
					sv.Unstable++
				}
			}
			v.Sets = append(v.Sets, sv)
		}
	}
	if reps, err := s.eng.CaptureReports(project); err == nil && reps != nil {
		for i := range reps {
			reps[i].Dir = "" // the server's paths stay in the server
		}
		v.Reports = reps
	}
	return writeJSON(w, http.StatusOK, v)
}

const keepCaptureRuns = 10

type captureProgress struct {
	Label string `json:"label"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	State string `json:"state,omitempty"` // up-to-date when a saved set was reused
}

// captureRun is what a capture has done so far, so the interface can show how
// far it is while the operation runs (the same numbers the CLI prints).
type captureRun struct {
	mu       sync.Mutex
	project  string
	progress []*captureProgress
	lines    []string
}

func (c *captureRun) of(label string) *captureProgress {
	for _, p := range c.progress {
		if p.Label == label {
			return p
		}
	}
	p := &captureProgress{Label: label}
	c.progress = append(c.progress, p)
	return p
}

func (c *captureRun) update(ev engine.CaptureEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch ev.Kind {
	case "start":
		c.of(ev.Label).Total = ev.Total
	case "shot":
		c.of(ev.Label).Done++
	case "up-to-date":
		c.of(ev.Label).State = "up-to-date"
	}
	var b strings.Builder
	captureLine(&b, ev)
	if line := strings.TrimSpace(b.String()); line != "" {
		c.lines = append(c.lines, line)
		if len(c.lines) > 200 {
			c.lines = c.lines[len(c.lines)-200:]
		}
	}
}

type captureRunView struct {
	Operation opView            `json:"operation"`
	Progress  []captureProgress `json:"progress"`
	Lines     []string          `json:"lines"`
}

type captureRuns struct {
	mu    sync.Mutex
	byID  map[string]*captureRun
	order []string
}

func (c *captureRuns) add(id string, r *captureRun) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID == nil {
		c.byID = map[string]*captureRun{}
	}
	c.byID[id] = r
	c.order = append(c.order, id)
	for len(c.order) > keepCaptureRuns {
		delete(c.byID, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *captureRuns) get(id string) *captureRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byID[id]
}

func (s *Server) handleCaptureRun(w http.ResponseWriter, r *http.Request, _ *session) error {
	id := r.PathValue("op")
	op, cr := s.ops.get(id), s.captureRuns.get(id)
	if op == nil || cr == nil || cr.project != r.PathValue("p") {
		return &Error{Status: http.StatusNotFound, Code: "operation_not_found", Message: "no capture " + quote(id)}
	}
	cr.mu.Lock()
	v := captureRunView{Operation: op.view(), Progress: []captureProgress{}, Lines: append([]string{}, cr.lines...)}
	for _, p := range cr.progress {
		v.Progress = append(v.Progress, *p)
	}
	cr.mu.Unlock()
	return writeJSON(w, http.StatusOK, v)
}

func captureLine(w io.Writer, ev engine.CaptureEvent) {
	switch {
	case ev.Text != "":
		fmt.Fprintf(w, "%s\n", ev.Text)
	case ev.Kind == "shot" && ev.Shot != nil:
		fmt.Fprintf(w, "%s: %s#%s %s %s\n", ev.Label, ev.Shot.Screen, ev.Shot.State, ev.Shot.Viewport, ev.Shot.Status)
	case ev.Kind == "serve":
		fmt.Fprintf(w, "%s: %s\n", ev.Label, ev.URL)
	default:
		fmt.Fprintf(w, "%s %s\n", ev.Kind, ev.Label)
	}
}

// handleCaptureCompare captures two branches (or takes two saved sets),
// compares them and writes the reports, as an operation.
func (s *Server) handleCaptureCompare(w http.ResponseWriter, r *http.Request, sess *session) error {
	project := r.PathValue("p")
	var req struct {
		A       string `json:"a"`
		B       string `json:"b"`
		Mode    string `json:"mode"`
		Lang    string `json:"lang"`
		Again   bool   `json:"again"`
		UseSets bool   `json:"use_sets"`
		Approve bool   `json:"approve"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	p, err := s.eng.Project(project)
	if err != nil {
		return projectNotFound(project)
	}
	if strings.TrimSpace(req.A) == "" || strings.TrimSpace(req.B) == "" {
		return badRequest("a and b are required: the branches or sets to compare")
	}
	if req.Mode != "" && req.Mode != "mock" && req.Mode != "real" && req.Mode != "grabar" {
		return badRequest("mode must be mock, real or grabar")
	}
	if req.Lang != "" && req.Lang != capture.LangEN && req.Lang != capture.LangES {
		return badRequest("lang must be en or es")
	}
	if !req.UseSets && !req.Approve {
		var cmds []string
		for _, sel := range []string{req.A, req.B} {
			_, c, err := s.eng.CaptureApproval(r.Context(), engine.CaptureTarget{Project: p.Name, Selector: sel})
			if err != nil {
				return captureError(err)
			}
			cmds = append(cmds, c...)
		}
		if len(cmds) > 0 {
			e := errf(http.StatusConflict, "run_not_approved", "the commands that start the application have not been approved")
			e.Hint = "approve them once; they run code from the repository"
			e.Affected = nil
			e.Original = strings.Join(cmds, "\n")
			return e
		}
	}
	cr := &captureRun{project: p.Name}
	run := func(ctx context.Context, stdout, _ io.Writer) (string, error) {
		defer s.hub.publish("state.changed", map[string]string{"project": p.Name})
		res, err := s.eng.CapturePair(ctx, engine.ComparisonRequest{
			Project: p.Name, A: engine.CaptureTarget{Project: p.Name, Selector: req.A, Label: req.A},
			B:    engine.CaptureTarget{Project: p.Name, Selector: req.B, Label: req.B},
			Mode: req.Mode, Again: req.Again, Approve: true, UseSets: req.UseSets, Lang: req.Lang,
		}, func(ev engine.CaptureEvent) { cr.update(ev); captureLine(stdout, ev) })
		if err != nil {
			return "", captureError(err)
		}
		c := res.Comparison.Counts
		return fmt.Sprintf("%s vs %s: %d changed, %d same, %d new, %d removed", req.A, req.B, c.Changed, c.Same, c.New, c.Removed), nil
	}
	op, replay, err := s.ops.start(startRequest{kind: "capture.compare", project: p.Name, key: "capture|" + p.Name,
		session: sess.id, idemKey: r.Header.Get("Idempotency-Key"), run: run})
	if err != nil {
		return err
	}
	if !replay {
		s.captureRuns.add(op.ID, cr)
	}
	w.Header().Set("Location", apiBase+"/projects/"+p.Name+"/capture/runs/"+op.ID)
	return writeJSON(w, http.StatusAccepted, op.view())
}

func captureError(err error) error {
	switch {
	case errors.Is(err, capture.ErrEngineMissing):
		e := errf(http.StatusConflict, "capture_engine_missing", err.Error())
		e.Hint = "install it from the Tools page or run `theo tool capture setup`"
		return e
	case errors.Is(err, capture.ErrNoInventory):
		return errf(http.StatusConflict, "capture_no_inventory", err.Error())
	case errors.Is(err, engine.ErrRunNotApproved):
		return errf(http.StatusConflict, "run_not_approved", err.Error())
	}
	return err
}

// handleCaptureSetup installs the capture engine (Node's Playwright and
// Chromium) as an operation.
func (s *Server) handleCaptureSetup(w http.ResponseWriter, r *http.Request, sess *session) error {
	run := func(ctx context.Context, stdout, _ io.Writer) (string, error) {
		if err := s.eng.CaptureEngine().Install(ctx, stdout); err != nil {
			return "", err
		}
		st, err := s.eng.CaptureEngine().Check(ctx)
		if err != nil {
			return "", err
		}
		return "Capture engine ready: chromium " + st.Chromium, nil
	}
	op, _, err := s.ops.start(startRequest{kind: "capture.setup", key: "capture-setup", session: sess.id,
		idemKey: r.Header.Get("Idempotency-Key"), run: run})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, op.view())
}

var captureTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".png": "image/png", ".md": "text/markdown; charset=utf-8",
	".pdf": "application/pdf", ".json": "application/json",
}

// handleCaptureFile serves a file of a report folder. Only the report's own
// files, only with these extensions, and a CSP that allows no connection: the
// report is made from the repository's data.
func (s *Server) handleCaptureFile(w http.ResponseWriter, r *http.Request, _ *session) error {
	project, name, file := r.PathValue("p"), r.PathValue("name"), r.PathValue("file")
	root, err := s.eng.CaptureRoot(project)
	if err != nil {
		return projectNotFound(project)
	}
	notFound := errf(http.StatusNotFound, "report_not_found", "no such report file")
	if name == "" || name != path.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `\`) ||
		file == "" || strings.ContainsRune(file, '\\') || path.Clean(file) != file || strings.HasPrefix(file, "..") {
		return notFound
	}
	ctype, ok := captureTypes[strings.ToLower(path.Ext(file))]
	if !ok {
		return notFound
	}
	full := filepath.Join(root, filepath.FromSlash(capture.ReportsDir), name, filepath.FromSlash(file))
	f, err := os.Open(full)
	if err != nil {
		return notFound
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return notFound
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	// The page has to be able to load its own pictures with the session cookie, so it cannot be a sandbox with an opaque
	// origin. What it may do instead is what it needs: its pictures, its inline style and its inline filter script, and
	// no connection of any kind (nothing can call the API or send anything out), no frames, no forms, no <base>.
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; font-src data:; connect-src 'none'; frame-src 'none'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'")
	http.ServeContent(w, r, "", st.ModTime(), f)
	return nil
}
