package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// projectSubcommands are the `theo project` subcommands that live in their
// own file; runProject looks them up before its own switch.
var projectSubcommands = map[string]func(e *engine.Engine, args []string, stdout io.Writer) error{
	"show":   runProjectShow,
	"rename": runProjectRename,
	"remove": runProjectRemove,
}

func init() {
	registerHelp(20, `
Projects and Compose files:
  project show <name> [--json]                 All the settings of a project
  project rename <old> <new> [--yes]           Rename a project and migrate its records (instance
                                               settings, history). Refused if the new name is taken
                                               or its instances have containers: their Compose
                                               project names derive from the project name
  project remove <name> [--yes] [--forget-records] [--json]
                                               Lists what keeps existing (worktrees, containers,
                                               volumes, generated files) and unregisters. Refused
                                               while instances have containers, unless --yes.
                                               --forget-records also drops Theo's settings, port
                                               reservations and generated files (history stays)
  project set <name> --compose-file <f> [--compose-file <f>...] | --default-compose-files
                                               Compose files of the project, relative to each
                                               worktree, in the order of docker compose -f A -f B
  --compose-file <f> (repeatable) on up, down, restart, ps, logs, env, volumes, services, ports and
  instance overrides the project's Compose files for that command only.
  instance set <project> [-w <w>] --profile <p> [--profile <p>...] | --no-profiles
                                               Compose profiles the instance always starts with,
                                               in addition to up --profile
`)
}

func runProjectShow(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("project show needs exactly one project name")
	}
	info, err := e.ProjectInfo(pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, info)
	}
	envFile := "(the main worktree's .env)"
	if info.EnvFile != "" {
		envFile = info.EnvFile
	}
	files := "(found by Compose: compose.yaml or docker-compose.yml, and its override)"
	if len(info.ComposeFiles) > 0 {
		files = strings.Join(info.ComposeFiles, ", ")
	}
	dir := info.WorktreeDirEffective + " (Theo's own)"
	if info.WorktreeDir != "" {
		dir = info.WorktreeDir
	}
	fmt.Fprintf(stdout, "Project        %s\n", info.Name)
	fmt.Fprintf(stdout, "Path           %s\n", info.Path)
	fmt.Fprintf(stdout, "Registered     %s\n", info.AddedAt.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(stdout, "Env file       %s\n", envFile)
	fmt.Fprintf(stdout, "Compose files  %s\n", files)
	fmt.Fprintf(stdout, "Worktree dir   %s\n", dir)
	return nil
}

func runProjectRename(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project rename", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "rename although volumes kept under the old Compose project names will not be used")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("project rename needs the current name and the new name")
	}
	res, err := e.RenameProject(context.Background(), pos[0], pos[1], engine.RenameOptions{Yes: *yes})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "Renamed project %q to %q (%d record(s) migrated).\n", res.Old, res.New, res.Records)
	for _, n := range res.Notes {
		fmt.Fprintf(stdout, "Note: %s\n", n)
	}
	return nil
}

func runProjectRemove(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project remove", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "remove although instances have containers")
	forget := fs.Bool("forget-records", false, "also drop Theo's settings, port reservations and generated files for the project")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("project remove needs exactly one project name")
	}
	res, err := e.RemoveProjectChecked(context.Background(), pos[0], engine.RemoveProjectOptions{Yes: *yes, ForgetRecords: *forget})
	var busy *engine.ContainersError
	removed := err == nil
	if res == nil || (err != nil && !errors.As(err, &busy)) {
		return err
	}
	if *asJSON {
		if werr := writeJSON(stdout, struct {
			Removed        bool                     `json:"removed"`
			RecordsDropped bool                     `json:"records_dropped"`
			Resources      *engine.ProjectResources `json:"resources"`
		}{removed, removed && *forget, res}); werr != nil {
			return werr
		}
		return err
	}
	printResources(stdout, res, *forget, removed)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nRemoved project %q from the registry. Its files, containers and volumes were not touched (%s).\n", res.Project, res.Path)
	if *forget {
		fmt.Fprintln(stdout, "Theo's own settings, port reservations and generated files for it were forgotten; the history stays.")
	}
	return nil
}

// printResources says what keeps existing after a project is removed.
func printResources(w io.Writer, res *engine.ProjectResources, forget, removed bool) {
	verb := "stays"
	if !removed {
		verb = "would stay"
	}
	fmt.Fprintf(w, "Project %q (%s). Removing it from the registry deletes nothing; this %s:\n", res.Project, res.Path, verb)
	if res.DockerError != "" {
		fmt.Fprintf(w, "  Docker could not be checked, so containers and volumes are unknown: %s\n", firstLine(res.DockerError))
	}
	section := func(label string, items []string) {
		if len(items) == 0 {
			fmt.Fprintf(w, "  %s: none\n", label)
			return
		}
		fmt.Fprintf(w, "  %s:\n", label)
		for _, it := range items {
			fmt.Fprintf(w, "    %s\n", it)
		}
	}
	section("worktrees on disk", res.Worktrees)
	stacks := make([]string, len(res.Stacks))
	for i, s := range res.Stacks {
		stacks[i] = fmt.Sprintf("%s (%s) in %s", s.Name, s.Status, s.Worktree)
	}
	section("Compose projects with containers", stacks)
	section("volumes kept with their data", res.Volumes)
	files := "generated files (kept)"
	if forget {
		files = "generated files (deleted: they are Theo's own)"
	}
	section(files, res.GeneratedFiles)
	recs := fmt.Sprintf("%d instance settings, %d reserved ports", res.Records.Settings, res.Records.Ports)
	if forget {
		fmt.Fprintf(w, "  Theo's records: %s forgotten; %d history events stay\n", recs, res.Records.History)
	} else {
		fmt.Fprintf(w, "  Theo's records: %s, %d history events kept (--forget-records drops the first two)\n", recs, res.Records.History)
	}
}
