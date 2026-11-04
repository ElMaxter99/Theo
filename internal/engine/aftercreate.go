package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

// ErrSetupNotApproved means the after_create commands of a repository have
// not been approved yet.
var ErrSetupNotApproved = errors.New("the commands of after_create have not been approved for this project")

// SetupPlan is what a repository asks to run after creating a worktree.
type SetupPlan struct {
	Project  string   `json:"project"`
	Path     string   `json:"path"`
	Commands []string `json:"commands"`
	// Trusted is true when the person already approved exactly these
	// commands for the project.
	Trusted bool `json:"trusted"`
}

// SetupResult is what running them did.
type SetupResult struct {
	SetupPlan
	Ran    []string `json:"ran,omitempty"`
	Failed string   `json:"failed,omitempty"` // the command that failed, if any
	Error  string   `json:"error,omitempty"`
}

// PlanSetup reads the after_create commands from the worktree at path (the
// branch's own .theo.json) and says whether they are approved.
func (e *Engine) PlanSetup(project, path string) (SetupPlan, error) {
	p, err := e.Project(project)
	if err != nil {
		return SetupPlan{}, err
	}
	decl, err := repoconfig.Load(path)
	if err != nil {
		return SetupPlan{}, err
	}
	plan := SetupPlan{Project: p.Name, Path: path, Commands: decl.AfterCreate}
	plan.Trusted = len(plan.Commands) > 0 && p.SetupTrusted == repoconfig.Fingerprint(plan.Commands)
	return plan, nil
}

// RunSetup runs the after_create commands in the worktree, one after the
// other, stopping at the first that fails. They run code from the
// repository: without approve they only run if the person approved exactly
// these commands before. With approve the approval is remembered.
func (e *Engine) RunSetup(ctx context.Context, project, path string, approve bool, stdout, stderr io.Writer) (*SetupResult, error) {
	plan, err := e.PlanSetup(project, path)
	if err != nil {
		return nil, err
	}
	res := &SetupResult{SetupPlan: plan}
	if len(plan.Commands) == 0 {
		return res, nil
	}
	if !plan.Trusted && !approve {
		return res, ErrSetupNotApproved
	}
	if approve && !plan.Trusted && !execx.Explaining() {
		fp := repoconfig.Fingerprint(plan.Commands)
		if err := e.updateProject(plan.Project, func(sp *registry.Project) error { sp.SetupTrusted = fp; return nil }); err != nil {
			return res, fmt.Errorf("could not remember the approval: %w", err)
		}
		res.Trusted = true
	}
	for _, cmd := range plan.Commands {
		fmt.Fprintf(stderr, "theo: after_create: %s\n", cmd)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		name, args := shellCommand(cmd)
		err := execx.StreamChange(ctx, path, stdout, stderr, name, args...)
		cancel()
		if err != nil {
			res.Failed, res.Error = cmd, err.Error()
			return res, fmt.Errorf("after_create command %q failed: %w", cmd, err)
		}
		res.Ran = append(res.Ran, cmd)
	}
	return res, nil
}

// shellCommand is how a command line runs on this system.
func shellCommand(cmd string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", cmd}
	}
	return "sh", []string{"-c", cmd}
}

// WorkspacePath resolves a worktree of a project (branch, directory name or
// path) to its path.
func (e *Engine) WorkspacePath(ctx context.Context, project, selector string) (string, error) {
	p, err := e.Project(project)
	if err != nil {
		return "", err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return "", err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return "", fmt.Errorf("project %q: %w", p.Name, err)
	}
	return wt.Path, nil
}
