package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

// GitFastVersion is the Git that compares every branch with its base in one
// command, which makes listing many worktrees much faster.
var GitFastVersion = compose.Version{Major: 2, Minor: 41, Patch: 0}

// v5Checks are the checks added in 0.5. They only appear when they have
// something to say, so a healthy machine keeps its short report.
func v5Checks(env DoctorEnv, gitLine string) []Check {
	var out []Check
	if v, err := compose.ParseVersion(gitLine); err == nil && !v.AtLeast(GitFastVersion) {
		out = append(out, Check{ID: "git-speed", Name: "git speed", Status: StatusWarn,
			Detail: fmt.Sprintf("%s works, but Git %s or later compares branches in one command, which makes `theo worktrees` and `theo worktree clean` much faster with many worktrees", strings.TrimSpace(gitLine), GitFastVersion),
			Fix:    "upgrade Git: https://git-scm.com/downloads"})
	}
	if c, ok := checkRepoConfigs(env); ok {
		out = append(out, c)
	}
	if c, ok := checkClaudeHooks(env); ok {
		out = append(out, c)
	}
	if c, ok := checkPortLimits(env); ok {
		out = append(out, c)
	}
	return out
}

// checkRepoConfigs reads .theo.json of every registered project.
func checkRepoConfigs(env DoctorEnv) (Check, bool) {
	c := Check{ID: "repo-config", Name: repoconfig.FileName}
	var bad, using []string
	for _, root := range env.Projects {
		l, err := repoconfig.Load(root)
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("%s: %v", filepath.Base(root), err))
		case len(l.Sources) > 0:
			using = append(using, filepath.Base(root))
			c.Notes = append(c.Notes, l.Notes...)
		}
	}
	switch {
	case len(bad) > 0:
		c.Status = StatusWarn
		c.Detail = "cannot read: " + strings.Join(bad, "; ")
		c.Fix = "fix the JSON in the file; until then Theo ignores what it declares"
	case len(using) > 0:
		c.Status = StatusInfo
		c.Detail = "read for " + strings.Join(using, ", ")
	default:
		return c, false
	}
	return c, true
}

// checkClaudeHooks says whether Theo's hooks are registered in the user's
// Claude Code settings or in the settings of a registered project.
func checkClaudeHooks(env DoctorEnv) (Check, bool) {
	files := []string{}
	if env.Home != "" {
		files = append(files, filepath.Join(env.Home, ".claude", "settings.json"))
	}
	for _, root := range env.Projects {
		files = append(files, filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"))
	}
	if len(files) == 0 {
		return Check{}, false
	}
	c := Check{ID: "claude-hooks", Name: "Claude Code hooks"}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		for _, groups := range doc.Hooks {
			for _, g := range groups {
				for _, h := range g.Hooks {
					if strings.Contains(h.Command, " hook ") && strings.Contains(h.Command, "theo") {
						c.Status, c.Detail = StatusOK, "Theo's hooks are in "+f
						return c, true
					}
				}
			}
		}
	}
	c.Status = StatusInfo
	c.Detail = "not installed: Claude Code creates its worktrees without Theo, and Theo cannot tell what the agent does"
	c.Fix = "theo mcp install --hooks"
	return c, true
}
