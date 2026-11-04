package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/proxy"
)

func init() {
	registerHelp(46, `
  env [project] [-w <w>] [--format sh|powershell|dotenv|json]
                                               Print where this worktree runs, for a shell or a
                                               program: THEO_PROJECT, THEO_WORKTREE, THEO_WORKTREE_PATH,
                                               THEO_COMPOSE_PROJECT, THEO_PORT_<SERVICE>,
                                               THEO_URL_<SERVICE> and THEO_URL. Load it with
                                               eval "$(theo env)" (sh), theo env --format powershell |
                                               Invoke-Expression, or read --format json
`)
}

// isEnvExport says whether the arguments of `theo env` ask for the
// variables of a worktree instead of managing its settings (set, unset,
// list).
func isEnvExport(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "set", "unset", "list":
		return false
	}
	return true
}

// EnvVar is one exported variable.
type envVar struct{ Name, Value string }

// worktreeEnv builds the variables that say where an instance runs. A
// Compose file that cannot be read still gives the identity variables.
func worktreeEnv(ctx context.Context, e *engine.Engine, inst *engine.Instance) (vars []envVar, warning string) {
	name := inst.Branch
	if name == "" {
		name = inst.Workspace
	}
	vars = []envVar{
		{"THEO_PROJECT", inst.Project},
		{"THEO_WORKTREE", name},
		{"THEO_WORKTREE_PATH", inst.Workspace},
		{"THEO_COMPOSE_PROJECT", inst.Compose},
	}
	plan, err := e.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return vars, "ports unknown: " + firstLine(err.Error())
	}
	first := map[string]engine.PortMapping{} // lowest TCP container port per service
	for _, m := range plan.Mappings {
		if m.Protocol != "tcp" || m.URL() == "" {
			continue
		}
		if cur, ok := first[m.Service]; !ok || m.Target < cur.Target {
			first[m.Service] = m
		}
	}
	services := make([]string, 0, len(first))
	for s := range first {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, s := range services {
		m := first[s]
		port := m.Effective
		if m.Running != 0 {
			port = m.Running
		}
		vars = append(vars, envVar{"THEO_PORT_" + envName(s), fmt.Sprint(port)}, envVar{"THEO_URL_" + envName(s), m.URL()})
	}
	if def := proxy.DefaultService(services); def != "" {
		vars = append(vars, envVar{"THEO_URL", first[def].URL()})
	}
	return vars, ""
}

// envName turns a service name into the part of a variable name.
func envName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func runEnvExport(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	format := fs.String("format", "sh", "sh, powershell, dotenv or json")
	asJSON := fs.Bool("json", false, "same as --format json")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *asJSON {
		*format = "json"
	}
	if len(pos) > 1 {
		return usageErrorf("env takes at most one project name")
	}
	ctx := context.Background()
	project := ""
	if len(pos) == 1 && pos[0] != "." {
		project = pos[0]
	} else {
		here, _ := os.Getwd()
		name, wt, ok := projectForDir(ctx, e, here)
		if !ok {
			return fmt.Errorf("the current folder is not in a registered project; name one: theo env <project> -w <worktree>")
		}
		project = name
		if sel.worktree == "" {
			sel.worktree = wt.Path
		}
	}
	inst, err := e.InstanceFor(ctx, project, sel.worktree, sel.options())
	if err != nil {
		return err
	}
	vars, warning := worktreeEnv(ctx, e, inst)
	if warning != "" {
		fmt.Fprintf(stderr, "theo: warning: %s\n", warning)
	}
	switch *format {
	case "sh":
		for _, v := range vars {
			fmt.Fprintf(stdout, "export %s=%s\n", v.Name, shQuote(v.Value))
		}
	case "powershell":
		for _, v := range vars {
			fmt.Fprintf(stdout, "$env:%s = '%s'\n", v.Name, strings.ReplaceAll(v.Value, "'", "''"))
		}
	case "dotenv":
		for _, v := range vars {
			fmt.Fprintf(stdout, "%s=%q\n", v.Name, v.Value)
		}
	case "json":
		m := map[string]string{}
		for _, v := range vars {
			m[v.Name] = v.Value
		}
		return writeJSON(stdout, m)
	default:
		return usageErrorf("--format must be sh, powershell, dotenv or json")
	}
	return nil
}

// shQuote quotes a value for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
