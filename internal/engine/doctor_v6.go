package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// composeNames are the files Compose looks for in a folder.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// portFindings are the things in a project's Compose files that limit how
// Theo can give a branch its own ports.
type portFindings struct {
	ComposeFile   string // COMPOSE_FILE as set in the environment or the .env
	ContainerName []string
	VarPorts      []string
}

// scanComposePorts reads the Compose files of a folder as text. It is a
// heuristic (there is no YAML parser in Theo): it looks for `container_name:`
// and for `${...}` inside a `ports:` list, and never fails.
func scanComposePorts(root string) portFindings {
	var f portFindings
	for _, name := range composeNames {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		portsIndent := -1
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimRight(raw, "\r")
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if strings.HasPrefix(trim, "container_name:") {
				f.ContainerName = append(f.ContainerName, name)
			}
			if portsIndent >= 0 && indent <= portsIndent && !strings.HasPrefix(trim, "-") {
				portsIndent = -1
			}
			if strings.HasPrefix(trim, "ports:") {
				portsIndent = indent
				continue
			}
			if portsIndent >= 0 && strings.HasPrefix(trim, "-") && strings.Contains(trim, "${") {
				f.VarPorts = append(f.VarPorts, name)
			}
		}
	}
	return f
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// checkPortLimits says, for each registered project, what in its Compose
// files can get in the way of free host ports per branch, before `theo up`
// fails on it. It only reports; Theo handles fixed container names itself.
func checkPortLimits(env DoctorEnv) (Check, bool) {
	c := Check{ID: "port-limits", Name: "ports per branch", Status: StatusInfo}
	for _, root := range env.Projects {
		name := filepath.Base(root)
		f := scanComposePorts(root)
		f.ComposeFile = env.getenv("COMPOSE_FILE")
		if f.ComposeFile == "" {
			f.ComposeFile, _ = compose.EnvFileValue([]string{filepath.Join(root, ".env")}, "COMPOSE_FILE")
		}
		if f.ComposeFile != "" {
			c.Notes = append(c.Notes, fmt.Sprintf("%s: COMPOSE_FILE is set (%s); Theo checks it loads the same files as Compose and stops if not. Keep it in the .env that `copy` in .theo.json gives to each worktree", name, f.ComposeFile))
		}
		if files := uniq(f.ContainerName); len(files) > 0 {
			c.Notes = append(c.Notes, fmt.Sprintf("%s: fixed container_name in %s; Theo removes it per instance, but anything outside Theo that uses that name will not find the branch's containers", name, strings.Join(files, ", ")))
		}
		if files := uniq(f.VarPorts); len(files) > 0 {
			c.Notes = append(c.Notes, fmt.Sprintf("%s: ${VAR} in ports in %s; a new worktree has no .env until it is copied, so the port may come out different from the main one. Add .env to `copy` in .theo.json", name, strings.Join(files, ", ")))
		}
	}
	if len(c.Notes) == 0 {
		return c, false
	}
	c.Detail = "some projects have Compose settings that limit free ports per branch (details below); nothing is broken"
	c.Fix = "see docs/cli/ports.md#limits"
	return c, true
}
