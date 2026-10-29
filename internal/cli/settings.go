package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/instances"
)

func runEnv(e *engine.Engine, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("env needs a subcommand: set, unset or list")
	}
	sub := args[0]
	fs := flag.NewFlagSet("env "+sub, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var secret, showSecrets, asJSON bool
	var services stringList
	switch sub {
	case "set":
		fs.BoolVar(&secret, "secret", false, "mask the value when it is shown")
		fs.Var(&services, "service", "apply only to this service (repeatable; default: every service)")
	case "list":
		fs.BoolVar(&showSecrets, "show-secrets", false, "show the values of secret variables")
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	}
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	want := map[string]int{"set": 2, "unset": 2, "list": 1}[sub]
	if want == 0 {
		return usageErrorf("unknown env subcommand %q", sub)
	}
	if len(pos) != want {
		switch sub {
		case "set":
			return usageErrorf("env set needs a project and NAME=VALUE")
		case "unset":
			return usageErrorf("env unset needs a project and a variable name")
		}
		return usageErrorf("env list needs exactly one project name")
	}
	ctx := context.Background()
	inst, err := e.InstanceFor(ctx, pos[0], sel.worktree, sel.options())
	if err != nil {
		return err
	}

	switch sub {
	case "set":
		name, value, ok := strings.Cut(pos[1], "=")
		if !ok {
			return usageErrorf("env set needs NAME=VALUE, got %q", pos[1])
		}
		if err := e.SetEnv(inst, name, value, secret, services); err != nil {
			return err
		}
		scope := "every service"
		if len(services) > 0 {
			scope = strings.Join(services, ", ")
		}
		fmt.Fprintf(stdout, "Set %s for %s (%s). It applies the next time the instance starts; your files are not changed.\n", name, inst.Compose, scope)
	case "unset":
		found, err := e.UnsetEnv(inst, pos[1])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%s has no variable %s set with theo", inst.Compose, pos[1])
		}
		fmt.Fprintf(stdout, "Removed %s from %s. It applies the next time the instance starts.\n", pos[1], inst.Compose)
	case "list":
		cfg, err := e.Settings(inst)
		if err != nil {
			return err
		}
		if asJSON {
			masked := cfg
			masked.Env = map[string]instances.Var{}
			for k, v := range cfg.Env {
				if v.Secret && !showSecrets {
					v.Value = engine.Mask
				}
				masked.Env[k] = v
			}
			return writeJSON(stdout, masked.Env)
		}
		if len(cfg.Env) == 0 {
			fmt.Fprintln(stdout, "No variables set with theo for this instance.")
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tVALUE\tSERVICES")
		for _, k := range compose.SortedKeys(cfg.Env) {
			v := cfg.Env[k]
			value := v.Value
			if v.Secret && !showSecrets {
				value = engine.Mask
			}
			scope := "all"
			if len(v.Services) > 0 {
				scope = strings.Join(v.Services, ",")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", k, value, scope)
		}
		return tw.Flush()
	}
	return nil
}

func runInstanceCmd(e *engine.Engine, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("instance needs a subcommand: set, show or config")
	}
	sub := args[0]
	fs := flag.NewFlagSet("instance "+sub, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var neutralize, keep, auto, asJSON, noProfiles bool
	var profiles stringList
	switch sub {
	case "set":
		fs.BoolVar(&neutralize, "neutralize-container-names", false, "remove fixed container_name settings for this instance")
		fs.BoolVar(&keep, "keep-container-names", false, "keep the project's container_name settings")
		fs.Var(&profiles, "profile", "Compose profile the instance always starts with (repeatable)")
		fs.BoolVar(&noProfiles, "no-profiles", false, "clear the profiles of this instance")
	case "show":
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	case "config":
		fs.BoolVar(&auto, "auto-ports", false, "include the ports --auto-ports would assign")
		fs.Var(&profiles, "profile", "enable a Compose profile (repeatable)")
	default:
		return usageErrorf("unknown instance subcommand %q", sub)
	}
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("instance %s needs exactly one project name", sub)
	}
	ctx := context.Background()
	inst, err := e.InstanceFor(ctx, pos[0], sel.worktree, sel.options())
	if err != nil {
		return err
	}

	switch sub {
	case "set":
		setsProfiles := len(profiles) > 0 || noProfiles
		if neutralize && keep || len(profiles) > 0 && noProfiles || !neutralize && !keep && !setsProfiles {
			return usageErrorf("instance set needs --neutralize-container-names or --keep-container-names, and/or --profile <p> (repeatable) or --no-profiles")
		}
		if neutralize || keep {
			if err := e.SetNeutralizeContainerNames(inst, neutralize); err != nil {
				return err
			}
			if neutralize {
				fmt.Fprintf(stdout, "%s will start without fixed container names (the project's files are not changed).\n", inst.Compose)
			} else {
				fmt.Fprintf(stdout, "%s will keep the project's container names.\n", inst.Compose)
			}
		}
		if setsProfiles {
			if err := e.SetProfiles(inst, profiles); err != nil {
				return err
			}
			if len(inst.Profiles) == 0 {
				fmt.Fprintf(stdout, "%s starts without extra profiles (only the ones given to `theo up --profile`).\n", inst.Compose)
			} else {
				fmt.Fprintf(stdout, "%s always starts with the profile(s) %s, and with the ones given to `theo up --profile`.\n", inst.Compose, strings.Join(inst.Profiles, ", "))
			}
		}
	case "show":
		cfg, err := e.Settings(inst)
		if err != nil {
			return err
		}
		names := inst.FixedContainerNames()
		if asJSON {
			return writeJSON(stdout, struct {
				*engine.Instance
				Neutralize     bool              `json:"neutralize_container_names"`
				ContainerNames map[string]string `json:"fixed_container_names,omitempty"`
				Variables      []string          `json:"variables,omitempty"`
			}{inst, cfg.NeutralizeContainerNames, names, compose.SortedKeys(cfg.Env)})
		}
		describe(stdout, inst)
		fmt.Fprintf(stdout, "env file:        %s\n", envFileText(inst.EnvFile))
		fmt.Fprintf(stdout, "variables (theo): %s\n", listOrNone(compose.SortedKeys(cfg.Env)))
		fmt.Fprintf(stdout, "profiles (theo): %s\n", listOrNone(inst.Profiles))
		if len(inst.ComposeFiles) > 0 {
			fmt.Fprintf(stdout, "compose files:   %s (%s)\n", strings.Join(mapEach(inst.ComposeFiles, func(f string) string { return relative(inst.Workspace, f) }), ", "), inst.ComposeFilesSource)
		}
		fmt.Fprintf(stdout, "container names: %s\n", containerNamesText(names, cfg.NeutralizeContainerNames))
	case "config":
		text, err := e.ResolvedConfig(ctx, inst, profiles, auto)
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, text)
	}
	return nil
}

func envFileText(f engine.EnvFile) string {
	if f.Source == engine.EnvNone {
		return "none"
	}
	return fmt.Sprintf("%s (%s)", f.Path, f.Source)
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func containerNamesText(names map[string]string, neutralized bool) string {
	if len(names) == 0 {
		return "no service sets one"
	}
	var list []string
	for _, k := range compose.SortedKeys(names) {
		list = append(list, k+"="+names[k])
	}
	if neutralized {
		return "removed for this instance (" + strings.Join(list, ", ") + ")"
	}
	return "kept (" + strings.Join(list, ", ") + ")"
}

func runHistory(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	worktree := fs.String("worktree", "", "only this worktree")
	fs.StringVar(worktree, "w", "", "shorthand for --worktree")
	limit := fs.Int("limit", 20, "number of entries to show; 0 means all")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("history needs exactly one project name")
	}
	events, err := e.History(context.Background(), pos[0], *worktree, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, events)
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No operations recorded yet.")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tOPERATION\tRESULT\tINSTANCE\tUSER")
	for _, ev := range events {
		result := ev.Result
		if ev.Error != "" {
			result += ": " + firstLine(ev.Error)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", ev.Time.Local().Format("2006-01-02 15:04:05"), ev.Operation, result, ev.Compose, ev.User)
	}
	return tw.Flush()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 120 {
		line = line[:117] + "..."
	}
	return line
}
