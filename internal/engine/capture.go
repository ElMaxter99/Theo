package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

// CaptureEngine returns the capture engine of this Theo's data folder.
func (e *Engine) CaptureEngine() *capture.Engine { return capture.NewEngine(e.dataDir) }

// CaptureTarget is a worktree of a project to capture.
type CaptureTarget struct {
	Project string
	// Selector picks the worktree (branch, directory name or path); empty is the main one.
	Selector string
	// URL captures an application that is already running, instead of starting it.
	URL string
	// Label names the set; the default is the branch.
	Label string
}

// CaptureRequest is a capture of one deployment.
type CaptureRequest struct {
	CaptureTarget
	// Mode overrides the data mode of the inventory (mock, real or grabar).
	Mode string
	// Again captures even when a set of this commit already exists.
	Again bool
	// Approve approves the commands of the run section (see StartRun).
	Approve bool
}

// CaptureEvent tells how a capture goes, for the CLI and the interface.
type CaptureEvent struct {
	Kind  string // log, serve, start, shot, control, saved, up-to-date, matching, compare, reports, stopped
	Label string
	Text  string
	URL   string
	Total int
	Shot  *capture.Shot
	Set   *capture.Set
	Mode  string
}

// ErrNoInventory is returned when the worktree has no pantallas.json.
var ErrNoInventory = capture.ErrNoInventory

type captureDeployment struct {
	label    string
	path     string
	branch   string
	commit   string
	dirty    bool
	inv      *capture.Inventory
	plan     RunPlan
	hasPlan  bool
	url      string
	reuse    *capture.Set
	run      Running
	needsRun bool
	approved bool
}

// CaptureRoot is where sets and reports of a project are kept: its main folder.
func (e *Engine) CaptureRoot(project string) (string, error) {
	p, err := e.Project(project)
	if err != nil {
		return "", err
	}
	return p.Path, nil
}

func (e *Engine) captureDeployment(ctx context.Context, t CaptureTarget, again bool, mode string) (*captureDeployment, error) {
	p, err := e.Project(t.Project)
	if err != nil {
		return nil, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return nil, err
	}
	wt, err := selectWorktree(list, t.Selector)
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", p.Name, err)
	}
	d := &captureDeployment{path: wt.Path, branch: wt.Branch, commit: shortHash(wt.Head), url: t.URL}
	d.label = firstOf(t.Label, wt.Branch, filepath.Base(wt.Path))
	if dirty, derr := e.git.Dirty(ctx, wt.Path); derr == nil {
		d.dirty = dirty
	}
	if d.inv, err = capture.LoadInventory(wt.Path); err != nil {
		if errors.Is(err, capture.ErrNoInventory) {
			return nil, fmt.Errorf("%s in %s: %w", capture.InventoryPath, wt.Path, ErrNoInventory)
		}
		return nil, fmt.Errorf("%s: %w", capture.InventoryPath, err)
	}
	if errs := d.inv.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("%s is not valid (see `theo tool capture validate`):\n  %s", capture.InventoryPath, strings.Join(errs, "\n  "))
	}
	if !again && !d.dirty && mode == "" {
		if s, err := capture.FindSet(p.Path, d.label); err == nil && d.commit != "" && s.Commit == d.commit && s.App == d.inv.App {
			d.reuse = s
			return d, nil
		}
	}
	d.needsRun = t.URL == ""
	if d.needsRun {
		if d.plan, err = e.PlanRun(t.Project, wt.Path); err != nil {
			return nil, err
		}
		if d.plan.Spec == nil {
			return nil, fmt.Errorf("%s in %s has no run section: try `theo tool capture detect %s --write`, or capture a running app with --url", "run", wt.Path, wt.Path)
		}
		if d.plan.Spec.EffectiveKind() == "compose" {
			return nil, fmt.Errorf("the run section of %s is Compose: start it with `theo up %s` and capture it with --url", wt.Path, t.Project)
		}
		d.hasPlan = true
	}
	return d, nil
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// captureNeedsApproval lists the commands that were not approved yet.
func (d *captureDeployment) unapproved() []string {
	if d.hasPlan && !d.plan.Trusted {
		return d.plan.Commands
	}
	return nil
}

// CaptureApproval reports, for a target, the commands of its run section that
// the person has not approved (so the CLI can ask before anything runs).
func (e *Engine) CaptureApproval(ctx context.Context, t CaptureTarget) (path string, commands []string, err error) {
	d, err := e.captureDeployment(ctx, t, false, "")
	if err != nil {
		return "", nil, err
	}
	return d.path, d.unapproved(), nil
}

func (e *Engine) startCapture(ctx context.Context, d *captureDeployment, approve bool, emit func(CaptureEvent)) error {
	if d.url != "" || d.reuse != nil {
		return nil
	}
	w := &eventWriter{emit: emit, label: d.label}
	run, err := e.StartRun(ctx, d.plan, approve, io.Discard, w)
	if err != nil {
		return err
	}
	d.run = run
	d.url = run.URL()
	emit(CaptureEvent{Kind: "serve", Label: d.label, URL: d.url})
	return nil
}

// eventWriter turns what a build prints into log events, line by line.
type eventWriter struct {
	emit  func(CaptureEvent)
	label string
	part  string
}

func (w *eventWriter) Write(b []byte) (int, error) {
	w.part += string(b)
	for {
		i := strings.IndexByte(w.part, '\n')
		if i < 0 {
			return len(b), nil
		}
		w.emit(CaptureEvent{Kind: "log", Label: w.label, Text: strings.TrimRight(w.part[:i], "\r")})
		w.part = w.part[i+1:]
	}
}

func (e *Engine) captureOne(ctx context.Context, root string, d *captureDeployment, mode string, emit func(CaptureEvent)) (*capture.Set, error) {
	if d.reuse != nil {
		emit(CaptureEvent{Kind: "up-to-date", Label: d.label, Set: d.reuse})
		return d.reuse, nil
	}
	if err := ensureCaptureIgnore(root); err != nil {
		return nil, err
	}
	eng := e.CaptureEngine()
	set, err := eng.Capture(ctx, capture.CaptureOptions{
		Root: d.path, Inv: d.inv, BaseURL: d.url, Label: d.label, Commit: map[bool]string{true: "", false: d.commit}[d.dirty], Mode: mode,
		OutDir:  filepath.Join(root, filepath.FromSlash(capture.SetsDir), capture.SetFolder(d.label, d.commit)),
		Control: true,
		OnStart: func(total int) {
			sum := d.inv.Summarize()
			emit(CaptureEvent{Kind: "start", Label: d.label, Total: total, Mode: firstOf(mode, d.inv.Mocks.Modo, "mock"), Text: fmt.Sprintf("%d states x %d viewports", sum.States, sum.Viewports)})
		},
		OnShot: func(s capture.Shot) { emit(CaptureEvent{Kind: "shot", Label: d.label, Shot: &s}) },
	})
	if err != nil {
		return nil, err
	}
	emit(CaptureEvent{Kind: "saved", Label: d.label, Set: set})
	return set, nil
}

// ensureCaptureIgnore keeps the captures and reports out of git; the
// inventory next to them stays versioned.
func ensureCaptureIgnore(root string) error {
	dir := filepath.Join(root, ".theo", "capturas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte("conjuntos/\ninformes/\n"), 0o644)
}

// CaptureRun captures one deployment and saves its set. It returns the set and
// whether it was already up to date.
func (e *Engine) CaptureRun(ctx context.Context, req CaptureRequest, emit func(CaptureEvent)) (*capture.Set, error) {
	root, err := e.CaptureRoot(req.Project)
	if err != nil {
		return nil, err
	}
	d, err := e.captureDeployment(ctx, req.CaptureTarget, req.Again, req.Mode)
	if err != nil {
		return nil, err
	}
	if len(d.unapproved()) > 0 && !req.Approve {
		return nil, ErrRunNotApproved
	}
	if err := e.startCapture(ctx, d, req.Approve, emit); err != nil {
		return nil, err
	}
	defer func() {
		if d.run != nil {
			d.run.Stop()
			emit(CaptureEvent{Kind: "stopped", Label: d.label})
		}
	}()
	return e.captureOne(ctx, root, d, req.Mode, emit)
}

// ComparisonRequest is a comparison of two sets, captured now or already saved.
type ComparisonRequest struct {
	Project string
	A, B    CaptureTarget // only the Selector and Label are used when capturing
	Mode    string
	Again   bool
	Approve bool
	// UseSets compares the sets with the labels A.Label and B.Label as they are.
	UseSets bool
	Lang    string
	// Renderer lets reports render images; nil makes no images (tests).
	NoRender bool
	// NoAI leaves the screens the rules could not pair without a pair, instead
	// of asking an AI (0.10). Without chosen roles or consent AI is not asked either.
	NoAI bool
}

// CompareResult is what a comparison produced.
type CompareResult struct {
	Comparison *capture.Comparison
	Files      capture.ReportFiles
	A, B       *capture.Set
}

// CapturePair captures two deployments (or takes saved sets), compares them and
// writes the reports.
func (e *Engine) CapturePair(ctx context.Context, req ComparisonRequest, emit func(CaptureEvent)) (*CompareResult, error) {
	root, err := e.CaptureRoot(req.Project)
	if err != nil {
		return nil, err
	}
	var sa, sb *capture.Set
	var changed []string
	if req.UseSets {
		if sa, err = capture.FindSet(root, req.A.Label); err != nil {
			return nil, err
		}
		if sb, err = capture.FindSet(root, req.B.Label); err != nil {
			return nil, err
		}
	} else {
		a, err := e.captureDeployment(ctx, withProject(req.A, req.Project), req.Again, req.Mode)
		if err != nil {
			return nil, err
		}
		b, err := e.captureDeployment(ctx, withProject(req.B, req.Project), req.Again, req.Mode)
		if err != nil {
			return nil, err
		}
		for _, d := range []*captureDeployment{a, b} {
			if len(d.unapproved()) > 0 && !req.Approve {
				return nil, ErrRunNotApproved
			}
		}
		stop := func(d *captureDeployment) {
			if d.run != nil {
				d.run.Stop()
			}
		}
		defer stop(a)
		defer stop(b)
		if err := e.startCapture(ctx, a, req.Approve, emit); err != nil {
			return nil, err
		}
		if err := e.startCapture(ctx, b, req.Approve, emit); err != nil {
			return nil, err
		}
		if sa, err = e.captureOne(ctx, root, a, req.Mode, emit); err != nil {
			return nil, err
		}
		if sb, err = e.captureOne(ctx, root, b, req.Mode, emit); err != nil {
			return nil, err
		}
		changed = e.changedFiles(ctx, a.path, a.commit, b.commit)
		stop(a)
		stop(b)
		if a.run != nil || b.run != nil {
			emit(CaptureEvent{Kind: "stopped", Text: "both"})
		}
	}
	if changed == nil && !req.UseSets {
		changed = []string{}
	}
	if req.UseSets {
		changed = e.changedFiles(ctx, root, sa.Commit, sb.Commit)
	}
	pf, perr := capture.LoadPairs(root)
	if perr != nil {
		return nil, perr
	}
	key := capture.PairKey(sa.Label, sb.Label)
	var manual []capture.Pair
	for _, p := range pf[key] {
		if p.Method == capture.ManualPair {
			manual = append(manual, p)
		}
	}
	pairs, _, _ := capture.MatchScreens(&sa.Manifest, &sb.Manifest, manual)
	byMethod := map[string]int{}
	for _, p := range pairs {
		byMethod[p.Method]++
	}
	removed, added := capture.UnmatchedScreens(&sa.Manifest, &sb.Manifest, manual)
	look := 0
	if len(added) > 0 {
		look = len(removed)
	}
	text := fmt.Sprintf("%d by id, %d by route, %d by file", byMethod[capture.ByID], byMethod[capture.ByRoute], byMethod[capture.ByFile])
	if byMethod[capture.ManualPair] > 0 {
		text += fmt.Sprintf(", %d by hand", byMethod[capture.ManualPair])
	}
	if look > 0 {
		text += fmt.Sprintf(", %d need a look", look)
	}
	emit(CaptureEvent{Kind: "matching", Text: text, Total: len(pairs)})
	aiPairs := e.pairWithAI(ctx, req, root, key, pf, removed, added, emit)
	for _, p := range pf.Applicable(key) {
		if p.Method == capture.ManualPair {
			aiPairs = append(aiPairs, p)
		}
	}
	date := e.now()
	dir := capture.ReportFolder(root, date, sa.Label, sb.Label)
	if err := ensureCaptureIgnore(root); err != nil {
		return nil, err
	}
	cmp, err := capture.Compare(sa, sb, capture.CompareOptions{OutDir: dir, ChangedFiles: changed, AIPairs: aiPairs})
	if err != nil {
		return nil, err
	}
	emit(CaptureEvent{Kind: "compare", Label: sa.Label + " " + sb.Label})
	lang := capture.ResolveLang(req.Lang, e.Prefs().Get("language"))
	var rend capture.Renderer
	if !req.NoRender {
		r, err := e.CaptureEngine().NewRenderer(ctx)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		rend = r
	}
	files, err := capture.WriteComparisonReport(dir, sa, sb, cmp, lang, rend, nil)
	if err != nil {
		return nil, err
	}
	if h, pdf, err := capture.WriteCorporateReport(dir, sb, root, lang, rend); err == nil {
		files.Corporate, files.CorporatePDF = filepath.Base(h), ""
		if pdf != "" {
			files.CorporatePDF = filepath.Base(pdf)
		}
	} else {
		return nil, err
	}
	emit(CaptureEvent{Kind: "reports", Text: dir})
	return &CompareResult{Comparison: cmp, Files: files, A: sa, B: sb}, nil
}

func withProject(t CaptureTarget, project string) CaptureTarget {
	t.Project = project
	return t
}

func (e *Engine) changedFiles(ctx context.Context, dir, a, b string) []string {
	if a == "" || b == "" || a == b {
		return nil
	}
	out, err := execx.Run(ctx, dir, "git", "diff", "--name-only", a+"..."+b)
	if err != nil {
		return nil
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files
}

// CaptureCorporate writes the corporate report of a saved set.
func (e *Engine) CaptureCorporate(ctx context.Context, project, label, lang string, render bool) (html, pdf string, err error) {
	root, err := e.CaptureRoot(project)
	if err != nil {
		return "", "", err
	}
	set, err := capture.FindSet(root, label)
	if err != nil {
		return "", "", err
	}
	var rend capture.Renderer
	if render {
		r, err := e.CaptureEngine().NewRenderer(ctx)
		if err != nil {
			return "", "", err
		}
		defer r.Close()
		rend = r
	}
	dir := capture.ReportFolder(root, e.now(), set.Label)
	return capture.WriteCorporateReport(dir, set, root, capture.ResolveLang(lang, e.Prefs().Get("language")), rend)
}

// CaptureReport is one report folder found on disk.
type CaptureReport struct {
	Name      string          `json:"name"`
	Dir       string          `json:"dir"`
	A         string          `json:"a,omitempty"`
	B         string          `json:"b,omitempty"`
	Date      string          `json:"date"`
	Counts    *capture.Counts `json:"counts,omitempty"`
	Summary   bool            `json:"summary"`
	Gallery   bool            `json:"gallery"`
	Corporate bool            `json:"corporate"`
}

// CaptureReports lists the report folders of a project, newest first.
func (e *Engine) CaptureReports(project string) ([]CaptureReport, error) {
	root, err := e.CaptureRoot(project)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(root, filepath.FromSlash(capture.ReportsDir))
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CaptureReport
	for i := len(entries) - 1; i >= 0; i-- {
		en := entries[i]
		if !en.IsDir() {
			continue
		}
		r := CaptureReport{Name: en.Name(), Dir: filepath.Join(base, en.Name())}
		if len(en.Name()) >= 10 {
			r.Date = en.Name()[:10]
		}
		has := func(f string) bool { _, err := os.Stat(filepath.Join(r.Dir, f)); return err == nil }
		r.Summary, r.Gallery, r.Corporate = has("resumen.md"), has("composiciones.html"), has("corporativo.html")
		if data, err := os.ReadFile(filepath.Join(r.Dir, "comparacion.json")); err == nil {
			var c capture.Comparison
			if json.Unmarshal(data, &c) == nil {
				r.A, r.B, r.Counts = c.A.Label, c.B.Label, &c.Counts
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// CaptureSets lists the sets of a project, newest first.
func (e *Engine) CaptureSets(project string) ([]*capture.Set, error) {
	root, err := e.CaptureRoot(project)
	if err != nil {
		return nil, err
	}
	return capture.ListSets(root)
}
