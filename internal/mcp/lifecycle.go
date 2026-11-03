package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// heartbeat records that the server's owner is working in a worktree it
// owns, so Theo counts that worktree as in use for half an hour. It is best
// effort and never fails a call; a read-only server records nothing.
func (s *Server) heartbeat(ctx context.Context, t *tool, a args) {
	if s.opts.ReadOnly || s.opts.Owner == "" || s.opts.Engine == nil {
		return
	}
	project, worktree := a.str("project"), a.str("worktree")
	if project == "" || worktree == "" || t.name == "finish_worktree" || t.name == "remove_worktree" {
		return
	}
	_ = s.opts.Engine.Touch(ctx, project, worktree, s.opts.Owner)
}

func runFinishWorktree(ctx context.Context, s *Server, a args) (*result, error) {
	project, selector := a.str("project"), a.str("worktree")
	owner := a.str("owner")
	if owner == "" {
		owner = s.opts.Owner
	}
	if owner == "" {
		return nil, fmt.Errorf("%w: say who you are with owner (only the owner of a worktree can finish it)", errBadArgs)
	}
	release, err := s.claim("project "+project, "worktree finish")
	if err != nil {
		return nil, err
	}
	defer release()
	ws, err := s.opts.Engine.FinishWorktree(ctx, project, selector, engine.FinishOptions{Owner: owner})
	if err != nil {
		return nil, err
	}
	how := "its branch is merged"
	if ws.MergedBy == "" {
		how = "its branch is pushed to " + ws.Upstream
	}
	summary := fmt.Sprintf("Finished %s: removed %s (%s). The branch %s and any volumes were kept.", ws.Branch, ws.Path, how, ws.Branch)
	return ok(summary, map[string]any{"path": ws.Path, "branch": ws.Branch, "merged_by": ws.MergedBy, "upstream": ws.Upstream}), nil
}

func runCleanWorktrees(ctx context.Context, s *Server, a args) (*result, error) {
	r, err := s.opts.Engine.Clean(ctx, a.str("project"), engine.CleanOptions{})
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, c := range r.Close {
		what := "can be closed"
		if c.Reason != "" {
			what = "stays: " + c.Reason
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s (%s)", c.Branch, c.State, what, c.Path))
	}
	for _, c := range r.Inactive {
		lines = append(lines, fmt.Sprintf("- %s [inactive, %d days] %s", c.Branch, c.IdleDays, c.Path))
	}
	summary := fmt.Sprintf("%d worktree(s) of %s can be closed, %d inactive. Nothing was changed; the user closes them with `theo worktree clean %s --confirm %s`.",
		len(r.Closable()), r.Project, len(r.Inactive), r.Project, r.Project)
	if len(lines) > 0 {
		summary += "\n" + strings.Join(lines, "\n")
	}
	return ok(summary, r), nil
}
