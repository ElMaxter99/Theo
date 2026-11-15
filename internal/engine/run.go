package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
	"github.com/ElMaxter99/Theo/internal/runner"
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

// StartScript starts the application of a "script" run section: it runs the
// install commands, then the start command on a port, and returns once the
// application answers. The caller stops it with Process.Stop. The commands run
// code from the repository: without approve they only run if the person
// approved exactly this section before. With approve the approval is
// remembered.
func (e *Engine) StartScript(ctx context.Context, plan RunPlan, approve bool, stdout, stderr io.Writer) (*runner.Process, error) {
	spec := plan.Spec
	switch {
	case spec == nil:
		return nil, fmt.Errorf("%s has no run section", repoconfig.FileName)
	case spec.EffectiveKind() != repoconfig.RunScript:
		return nil, fmt.Errorf("the run section is kind %q: start it with theo up", spec.EffectiveKind())
	}
	if !plan.Trusted && !approve {
		return nil, ErrRunNotApproved
	}
	if approve && !plan.Trusted && !execx.Explaining() {
		fp := repoconfig.RunFingerprint(*spec)
		if err := e.updateProject(plan.Project, func(sp *registry.Project) error { sp.RunTrusted = fp; return nil }); err != nil {
			return nil, fmt.Errorf("could not remember the approval: %w", err)
		}
	}
	for _, cmd := range spec.Install {
		fmt.Fprintf(stderr, "theo: run: install: %s\n", cmd)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		name, args := shellCommand(cmd)
		err := execx.StreamChange(cctx, plan.Path, stdout, stderr, name, args...)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("run: install command %q failed: %w", cmd, err)
		}
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
