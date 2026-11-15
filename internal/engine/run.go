package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
	"github.com/ElMaxter99/Theo/internal/runner"
	"github.com/ElMaxter99/Theo/internal/staticserve"
)

// ErrRunNotApproved means the install and start commands of the run section
// of .theo.json have not been approved for this project.
var ErrRunNotApproved = errors.New("the commands of the run section have not been approved for this project")

// RunPlan is what a worktree's run section asks to start.
type RunPlan struct {
	Project  string              `json:"project"`
	Path     string              `json:"path"`
	Spec     *repoconfig.RunSpec `json:"spec,omitempty"`
	Commands []string            `json:"commands,omitempty"`
	// Trusted is true when the person already approved exactly these
	// commands and environment for the project.
	Trusted bool `json:"trusted"`
}

// PlanRun reads the run section from the worktree at path (the branch's own
// .theo.json) and says whether it is approved. A worktree without a run
// section gives a plan with a nil Spec.
func (e *Engine) PlanRun(project, path string) (RunPlan, error) {
	p, err := e.Project(project)
	if err != nil {
		return RunPlan{}, err
	}
	decl, err := repoconfig.Load(path)
	if err != nil {
		return RunPlan{}, err
	}
	plan := RunPlan{Project: p.Name, Path: path, Spec: decl.Run}
	if decl.Run != nil {
		plan.Commands = decl.Run.Commands()
		plan.Trusted = len(plan.Commands) > 0 && p.RunTrusted == repoconfig.RunFingerprint(*decl.Run)
	}
	return plan, nil
}

// Running is an application started from a run section, whatever its kind.
type Running interface {
	URL() string
	ListenPort() int
	Done() <-chan struct{}
	Err() error
	Stop()
}

type scriptRunning struct{ *runner.Process }

func (s scriptRunning) ListenPort() int { return s.Port }

type staticRunning struct{ *staticserve.Server }

func (s staticRunning) ListenPort() int { return s.Port }

// StartRun starts the application of a "script" or "static" run section and
// returns once it answers. The caller stops it with Stop. The commands run code
// from the repository: without approve they only run if the person approved
// exactly this section before. With approve the approval is remembered.
func (e *Engine) StartRun(ctx context.Context, plan RunPlan, approve bool, stdout, stderr io.Writer) (Running, error) {
	if plan.Spec != nil && plan.Spec.EffectiveKind() == repoconfig.RunStatic {
		return e.startStatic(ctx, plan, approve, stdout, stderr)
	}
	p, err := e.StartScript(ctx, plan, approve, stdout, stderr)
	if err != nil {
		return nil, err
	}
	return scriptRunning{p}, nil
}

// StartScript starts the application of a "script" run section: it runs the
// install commands, then the start command on a port, and returns once the
// application answers. See StartRun for the approval.
func (e *Engine) StartScript(ctx context.Context, plan RunPlan, approve bool, stdout, stderr io.Writer) (*runner.Process, error) {
	spec := plan.Spec
	switch {
	case spec == nil:
		return nil, fmt.Errorf("%s has no run section", repoconfig.FileName)
	case spec.EffectiveKind() != repoconfig.RunScript:
		return nil, fmt.Errorf("the run section is kind %q: start it with `theo up %s`", spec.EffectiveKind(), plan.Project)
	}
	if err := e.approveRun(plan, approve); err != nil {
		return nil, err
	}
	if err := runSteps(ctx, plan, "install", spec.Install, nil, stdout, stderr); err != nil {
		return nil, err
	}
	port := 0
	if !spec.AutoPort() {
		port = spec.Port.Fixed
	}
	fmt.Fprintf(stderr, "theo: run: start: %s\n", spec.Start)
	return runner.Start(ctx, runner.Spec{
		Dir:          plan.Path,
		Command:      spec.Start,
		Env:          spec.Env,
		Port:         port,
		PortEnv:      spec.PortEnv(),
		ReadyPath:    spec.ReadyPath(),
		ReadyStatus:  spec.ReadyStatus(),
		ReadyTimeout: time.Duration(spec.ReadyTimeout()) * time.Second,
		Log:          stderr,
	})
}

func (e *Engine) approveRun(plan RunPlan, approve bool) error {
	if !plan.Trusted && !approve {
		return ErrRunNotApproved
	}
	if approve && !plan.Trusted && !execx.Explaining() {
		fp := repoconfig.RunFingerprint(*plan.Spec)
		if err := e.updateProject(plan.Project, func(sp *registry.Project) error { sp.RunTrusted = fp; return nil }); err != nil {
			return fmt.Errorf("could not remember the approval: %w", err)
		}
	}
	return nil
}

// runSteps runs shell commands in the worktree, one after the other. When one
// fails, the error carries the last lines it printed.
func runSteps(ctx context.Context, plan RunPlan, label string, cmds []string, env []string, stdout, stderr io.Writer) error {
	for _, cmd := range cmds {
		fmt.Fprintf(stderr, "theo: run: %s: %s\n", label, cmd)
		tail := &tailBuffer{max: 20}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		err := execx.ShellChangeEnv(cctx, plan.Path, env, io.MultiWriter(stdout, tail), io.MultiWriter(stderr, tail), cmd)
		cancel()
		if err != nil {
			why := err.Error()
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				why = ee.Error()
			}
			msg := fmt.Sprintf("run: %s command %q failed: %s", label, cmd, why)
			if t := tail.String(); t != "" {
				msg += "\nlast lines it printed:\n  " + strings.ReplaceAll(t, "\n", "\n  ")
			}
			return errors.New(msg)
		}
	}
	return nil
}

// tailBuffer keeps the last lines written to it.
type tailBuffer struct {
	max   int
	lines []string
	part  string
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.part += string(b)
	for {
		i := strings.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		if l := strings.TrimRight(t.part[:i], "\r"); strings.TrimSpace(l) != "" {
			t.lines = append(t.lines, l)
		}
		t.part = t.part[i+1:]
		if len(t.lines) > t.max {
			t.lines = t.lines[1:]
		}
	}
	if len(t.part) > 4096 {
		t.part = t.part[len(t.part)-4096:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	l := t.lines
	if strings.TrimSpace(t.part) != "" {
		l = append(append([]string{}, l...), t.part)
	}
	return strings.Join(l, "\n")
}

var baseHref = regexp.MustCompile(`(?i)<base\s[^>]*href\s*=\s*["']([^"']+)["']`)

// startStatic runs install and build, checks what the build left, and serves
// the folder under the base path on a port of its own.
func (e *Engine) startStatic(ctx context.Context, plan RunPlan, approve bool, stdout, stderr io.Writer) (Running, error) {
	spec := plan.Spec
	if err := e.approveRun(plan, approve); err != nil {
		return nil, err
	}
	if err := runSteps(ctx, plan, "install", spec.Install, nil, stdout, stderr); err != nil {
		return nil, err
	}
	var env []string
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+spec.Env[k])
	}
	if err := runSteps(ctx, plan, "build", spec.Build, env, stdout, stderr); err != nil {
		return nil, err
	}
	dirName := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(spec.Dir)), "/") + "/"
	dir := filepath.Join(plan.Path, spec.Dir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("the build did not leave the folder %s (run.dir) in %s", dirName, plan.Path)
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, fmt.Errorf("%s has no index.html: the app cannot start under %s/", dirName, spec.Base)
	}
	if m := baseHref.FindSubmatch(index); m != nil {
		if h := string(m[1]); strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
			fmt.Fprintf(stderr, "theo: warning: %sindex.html has <base href=%q>:\nthe app will load its files from that address, not from this server. Make the\nbuild use %s/ (for example with an environment variable in run.env)\n", dirName, h, spec.Base)
		}
	}
	port := 0
	if !spec.AutoPort() {
		port = spec.Port.Fixed
	}
	srv, err := staticserve.Start(staticserve.Config{Dir: dir, Base: spec.Base, SPA: spec.SPA, Proxy: spec.Proxy}, port)
	if err != nil {
		return nil, fmt.Errorf("run: could not serve %s: %w", dirName, err)
	}
	line := fmt.Sprintf("theo: run: serve: %s under %s", dirName, spec.Base)
	prefixes := make([]string, 0, len(spec.Proxy))
	for k := range spec.Proxy {
		prefixes = append(prefixes, k)
	}
	sort.Strings(prefixes)
	for _, k := range prefixes {
		line += ", " + k + " to " + spec.Proxy[k]
	}
	fmt.Fprintln(stderr, line)
	return staticRunning{srv}, nil
}
