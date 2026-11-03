package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/proxy"
	"github.com/ElMaxter99/Theo/internal/registry"
)

func init() {
	registerCommand("open", runOpen)
	registerHelp(47, `
  open <project> [-w <w>] [service] [--print]  Open a running service of the instance in the browser: its local
                                               domain while a proxy runs, else http://localhost:<port>. Without a
                                               service: web, app, api or frontend, else the first one with a
                                               port. --print only prints the address
`)
	registerCompletion(cmdSpec{name: "open", args: []string{"project", "service"}, flags: with(selFlags, flagSpec{names: []string{"--print"}})})
}

// openURL opens an address in the browser. Tests replace it.
var openURL = openBrowser

// proxyPortForCLI is the port of the running proxy, if any. Tests replace it.
var proxyPortForCLI = func() (int, bool) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return 0, false
	}
	return proxy.RunningPort(dir)
}

func runOpen(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	printOnly := fs.Bool("print", false, "print the address instead of opening it")
	project, services, err := serviceArgs("open", fs, args)
	if err != nil {
		return err
	}
	if len(services) > 1 {
		return usageErrorf("open takes at most one service")
	}
	service := ""
	if len(services) == 1 {
		service = services[0]
	}
	ctx := context.Background()
	inst, err := e.Instance(ctx, project, sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	if service != "" {
		if err := inst.CheckServices([]string{service}); err != nil {
			return err
		}
	}
	plan, err := e.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return err
	}
	url, err := pickOpenURL(inst, plan, service)
	if err != nil {
		return err
	}
	if *printOnly {
		fmt.Fprintln(stdout, url)
		return nil
	}
	fmt.Fprintln(stdout, url)
	if err := openURL(url); err != nil {
		return fmt.Errorf("could not open a browser (%v); open %s yourself", err, url)
	}
	return nil
}

// pickOpenURL chooses the address of a service of a running instance.
func pickOpenURL(inst *engine.Instance, plan *engine.PortPlan, service string) (string, error) {
	running := map[string]engine.PortMapping{} // lowest TCP container port per running service
	var statuses []engine.PortStatus
	for _, m := range plan.Mappings {
		if m.Protocol != "tcp" || m.Status != engine.PortRunning || m.URL() == "" {
			continue
		}
		if cur, ok := running[m.Service]; !ok || m.Target < cur.Target {
			running[m.Service] = m
		}
		statuses = append(statuses, engine.PortStatus{Service: m.Service, ContainerPort: m.Target, Protocol: m.Protocol, HostPort: m.Running, URL: m.URL(), Status: m.Status})
	}
	start := fmt.Sprintf("theo up %s -w %s", inst.Project, worktreeArg(inst))
	if len(running) == 0 {
		return "", fmt.Errorf("no service of %s publishes a running port on the host; start it with `%s`", inst.Compose, start)
	}
	names := make([]string, 0, len(running))
	for n := range running {
		names = append(names, n)
	}
	sort.Strings(names)
	named := service != ""
	if !named {
		service = proxy.DefaultService(names)
	}
	m, ok := running[service]
	if !ok {
		return "", fmt.Errorf("service %s of %s publishes no running port on the host (running with ports: %s); start it with `%s`",
			service, inst.Compose, strings.Join(names, ", "), start)
	}
	if port, on := proxyPortForCLI(); on {
		links := proxy.Links(proxy.InstanceTargets(proxy.Instance{
			Project: inst.Project, Worktree: inst.Workspace, Branch: inst.Branch, Main: inst.Main, ComposeProject: inst.Compose,
		}, statuses), port, 0)[inst.Workspace]
		for _, l := range links {
			// Without a service, the short form <instance>.<project>.
			if l.Service == service && l.Default != named {
				return l.URL, nil
			}
		}
	}
	return m.URL(), nil
}
