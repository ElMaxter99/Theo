package capture

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed engine/runner.mjs
var runnerSource []byte

// Engine is the capture engine: Node, Playwright and Chromium, installed on
// demand in Theo's data folder, and the small runner Theo launches with them.
type Engine struct {
	// Dir is where the engine lives (<data dir>/tools/capture).
	Dir string
	// Node is the node binary; empty looks it up in PATH.
	Node string
}

// NewEngine returns the engine of a Theo data folder.
func NewEngine(dataDir string) *Engine {
	return &Engine{Dir: filepath.Join(dataDir, "tools", "capture")}
}

// ErrEngineMissing means Node, Playwright or Chromium is not ready.
var ErrEngineMissing = errors.New("the capture engine is not installed: run `theo tool capture setup`")

// Status is what `setup` and `doctor` report about the engine.
type Status struct {
	Node     string `json:"node"`
	Chromium string `json:"chromium"`
}

func (e *Engine) node() (string, error) {
	if e.Node != "" {
		return e.Node, nil
	}
	return exec.LookPath("node")
}

// WriteRunner puts runner.mjs in the engine folder (Node resolves Playwright
// from there) and returns its path.
func (e *Engine) WriteRunner() (string, error) {
	if err := os.MkdirAll(e.Dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(e.Dir, "runner.mjs")
	if cur, err := os.ReadFile(p); err != nil || !bytes.Equal(cur, runnerSource) {
		if err := os.WriteFile(p, runnerSource, 0o644); err != nil {
			return "", err
		}
	}
	return p, nil
}

// Check starts Chromium once and reports the versions.
func (e *Engine) Check(ctx context.Context) (Status, error) {
	node, err := e.node()
	if err != nil {
		return Status{}, fmt.Errorf("%w (Node 20 or newer is needed)", ErrEngineMissing)
	}
	runner, err := e.WriteRunner()
	if err != nil {
		return Status{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, node, runner, "check")
	cmd.Dir = e.Dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	var st Status
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var ev struct {
			Event, Chromium, Node, Error string
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "check":
			st = Status{Node: ev.Node, Chromium: ev.Chromium}
		case "error":
			return Status{}, fmt.Errorf("%w: %s", ErrEngineMissing, ev.Error)
		}
	}
	if st.Chromium == "" {
		if runErr != nil {
			return Status{}, fmt.Errorf("%w: %v %s", ErrEngineMissing, runErr, strings.TrimSpace(errb.String()))
		}
		return Status{}, ErrEngineMissing
	}
	return st, nil
}

// Install installs Playwright and Chromium in the engine folder (they need the
// network). Output of the installers goes to out.
func (e *Engine) Install(ctx context.Context, out io.Writer) error {
	if _, err := e.node(); err != nil {
		return errors.New("Node 20 or newer is needed and was not found in PATH")
	}
	if _, err := e.WriteRunner(); err != nil {
		return err
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return errors.New("npm was not found in PATH")
	}
	steps := [][]string{
		{npm, "init", "-y", "--silent"},
		{npm, "install", "--no-audit", "--no-fund", "playwright@1.65.0"},
		{npm, "exec", "--", "playwright", "install", "chromium"},
	}
	for _, s := range steps {
		cmd := exec.CommandContext(ctx, s[0], s[1:]...)
		cmd.Dir = e.Dir
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(s[1:], " "), err)
		}
	}
	return nil
}

// CaptureConfig is what the runner receives.
type captureConfig struct {
	BaseURL      string            `json:"baseURL"`
	OutDir       string            `json:"outDir"`
	Root         string            `json:"root"`
	Data         string            `json:"data"`
	Control      bool              `json:"control"`
	StorageState string            `json:"storageState,omitempty"`
	MaxHeight    int               `json:"maxHeight,omitempty"`
	Files        map[string]string `json:"files"`
	Inventory    *Inventory        `json:"inventory"`
}

// CaptureOptions says what to capture.
type CaptureOptions struct {
	Root    string     // worktree that holds the mocks and the session
	Inv     *Inventory // inventory of the deployment being captured
	BaseURL string     // where its application answers, with its base path
	Label   string
	Commit  string
	Mode    string // mock, real or grabar; empty takes the inventory's, then mock
	OutDir  string // set folder
	// Control captures every state twice and marks the ones that differ as unstable.
	Control bool
	// OnShot is called for every screenshot as soon as it is known.
	OnShot func(Shot)
	// OnStart is called with the number of screenshots planned.
	OnStart func(total int)
}

// ControlThreshold is the percentage of pixels that may differ between two
// captures of the same state before it counts as unstable.
const ControlThreshold = 0.05

// Capture runs the capture of one deployment and writes its set.
func (e *Engine) Capture(ctx context.Context, o CaptureOptions) (*Set, error) {
	node, err := e.node()
	if err != nil {
		return nil, fmt.Errorf("%w (Node 20 or newer is needed)", ErrEngineMissing)
	}
	runner, err := e.WriteRunner()
	if err != nil {
		return nil, err
	}
	mode := firstNonEmpty(o.Mode, o.Inv.Mocks.Modo, "mock")
	cfg := captureConfig{BaseURL: o.BaseURL, OutDir: o.OutDir, Root: o.Root, Data: mode, Control: o.Control, Files: map[string]string{}, Inventory: o.Inv, MaxHeight: 8000}
	if o.Inv.Sesion != nil && o.Inv.Sesion.StorageState != "" {
		cfg.StorageState = filepath.Join(o.Root, filepath.FromSlash(o.Inv.Sesion.StorageState))
	}
	for _, p := range o.Inv.Pantallas {
		for _, s := range p.Estados {
			for _, v := range o.Inv.Viewports {
				cfg.Files[p.ID+"#"+s.ID+"#"+v.ID] = ShotFile(p.ID, s.ID, v.ID)
			}
		}
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return nil, err
	}
	cfgFile := filepath.Join(o.OutDir, "capture-config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgFile, data, 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(cfgFile)

	cmd := exec.CommandContext(ctx, node, runner, "capture", cfgFile)
	cmd.Dir = e.Dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	set := &Set{Dir: o.OutDir}
	set.Manifest = Manifest{Version: 1, Label: o.Label, App: o.Inv.App, Commit: o.Commit, Date: time.Now().UTC().Format(time.RFC3339), Mode: mode, Entry: o.Inv.Entrada, Viewports: o.Inv.Viewports, Flows: toFlows(o.Inv.Flujos)}
	for _, p := range o.Inv.Pantallas {
		si := ScreenInfo{ID: p.ID, Title: p.Titulo, Route: p.Ruta, Origin: p.Origen}
		for _, s := range p.Estados {
			si.States = append(si.States, s.ID)
		}
		set.Screens = append(set.Screens, si)
	}
	ignores := map[string][]string{}
	for _, p := range o.Inv.Pantallas {
		for _, ig := range p.Ignorar {
			ignores[p.ID] = append(ignores[p.ID], ig.Selector)
		}
	}
	var engineErr string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev struct {
			Event    string `json:"event"`
			Shots    int    `json:"shots"`
			Chromium string `json:"chromium"`
			Error    string `json:"error"`
			Screen   string `json:"screen"`
			State    string `json:"state"`
			Viewport string `json:"viewport"`
			File     string `json:"file"`
			Control  string `json:"control"`
			Status   string `json:"status"`
			Reason   string `json:"reason"`
			Masks    []Rect `json:"masks"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "start":
			set.Chromium = ev.Chromium
			if o.OnStart != nil {
				o.OnStart(ev.Shots)
			}
		case "error":
			engineErr = ev.Error
		case "shot":
			sh := Shot{Screen: ev.Screen, State: ev.State, Viewport: ev.Viewport, File: ev.File, Status: ev.Status, Reason: ev.Reason, Masks: ev.Masks}
			if ev.Status == ShotOK && ev.Control != "" {
				sh = controlShot(set, sh, ev.Control, ignores[sh.Screen])
			}
			set.Shots = append(set.Shots, sh)
			if o.OnShot != nil {
				o.OnShot(sh)
			}
		}
	}
	waitErr := cmd.Wait()
	if engineErr != "" {
		return nil, fmt.Errorf("%w: %s", ErrEngineMissing, engineErr)
	}
	if waitErr != nil && ctx.Err() == nil && len(set.Shots) == 0 {
		return nil, fmt.Errorf("the capture engine failed: %v %s", waitErr, strings.TrimSpace(errb.String()))
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return set, set.Save()
}

// controlShot compares the two captures of a state taken one after the other.
func controlShot(set *Set, sh Shot, control string, ignores []string) Shot {
	defer os.Remove(filepath.Join(set.Dir, filepath.FromSlash(control)))
	a, err1 := set.Image(sh.File)
	b, err2 := set.Image(control)
	if err1 != nil || err2 != nil {
		return sh
	}
	if d := Diff(a, b, DiffOptions{}); d.Percent > ControlThreshold {
		sh.Status = ShotUnstable
		if d2 := Diff(a, b, DiffOptions{Masks: sh.Masks}); len(ignores) > 0 && d2.Percent <= ControlThreshold {
			sh.Reason = "masked: " + strings.Join(ignores, ", ")
		} else {
			sh.Reason = fmt.Sprintf("differs between two captures (%.2f %%)", d.Percent)
		}
	}
	return sh
}

func toFlows(f []Flow) []Flow { return f }

// NodeRenderer renders HTML to PNG or PDF with the engine's browser. One
// Chromium serves every job, so a report with dozens of images is quick.
type NodeRenderer struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	mu     sync.Mutex
	next   int
	stderr bytes.Buffer
}

// NewRenderer starts the rendering process.
func (e *Engine) NewRenderer(ctx context.Context) (*NodeRenderer, error) {
	node, err := e.node()
	if err != nil {
		return nil, fmt.Errorf("%w (Node 20 or newer is needed)", ErrEngineMissing)
	}
	runner, err := e.WriteRunner()
	if err != nil {
		return nil, err
	}
	r := &NodeRenderer{}
	r.cmd = exec.CommandContext(ctx, node, runner, "serve")
	r.cmd.Dir = e.Dir
	r.cmd.Stderr = &r.stderr
	if r.in, err = r.cmd.StdinPipe(); err != nil {
		return nil, err
	}
	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := r.cmd.Start(); err != nil {
		return nil, err
	}
	r.out = bufio.NewScanner(stdout)
	r.out.Buffer(make([]byte, 1<<20), 1<<20)
	for r.out.Scan() {
		var ev struct{ Event, Error string }
		if json.Unmarshal(r.out.Bytes(), &ev) != nil {
			continue
		}
		if ev.Event == "ready" {
			return r, nil
		}
		if ev.Event == "error" {
			r.Close()
			return nil, fmt.Errorf("%w: %s", ErrEngineMissing, ev.Error)
		}
	}
	r.Close()
	return nil, fmt.Errorf("%w: %s", ErrEngineMissing, strings.TrimSpace(r.stderr.String()))
}

func (r *NodeRenderer) do(kind, html, out string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	absHTML, _ := filepath.Abs(html)
	absOut, _ := filepath.Abs(out)
	job, _ := json.Marshal(map[string]any{"id": r.next, "kind": kind, "html": absHTML, "out": absOut})
	if _, err := r.in.Write(append(job, '\n')); err != nil {
		return err
	}
	for r.out.Scan() {
		var res struct {
			ID    int    `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if json.Unmarshal(r.out.Bytes(), &res) != nil || res.ID != r.next {
			continue
		}
		if !res.OK {
			return errors.New(res.Error)
		}
		return nil
	}
	return errors.New("the rendering process ended")
}

// Screenshot renders an HTML file into a full-page PNG.
func (r *NodeRenderer) Screenshot(htmlFile, pngFile string) error {
	return r.do("png", htmlFile, pngFile)
}

// PDF renders an HTML file into a PDF.
func (r *NodeRenderer) PDF(htmlFile, pdfFile string) error { return r.do("pdf", htmlFile, pdfFile) }

// Close stops the rendering process.
func (r *NodeRenderer) Close() {
	if r.in != nil {
		r.in.Close()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		done := make(chan struct{})
		go func() { r.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			r.cmd.Process.Kill()
			<-done
		}
	}
}
