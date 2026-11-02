package proxy

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// FromStatus lists the TCP ports the instances of a status report publish
// on the host, as targets.
func FromStatus(r *engine.StatusReport) []Target {
	var out []Target
	for _, p := range r.Projects {
		for _, wt := range p.Worktrees {
			if wt.Instance == nil {
				continue
			}
			out = append(out, InstanceTargets(Instance{
				Project: p.Name, Worktree: wt.Path, Branch: wt.Branch, Main: wt.Kind == engine.KindMain,
				ComposeProject: wt.Instance.ComposeProject,
			}, wt.Instance.Ports)...)
		}
	}
	return out
}

// Instance names one instance for InstanceTargets.
type Instance struct {
	Project        string // registered project name
	Worktree       string // worktree path
	Branch         string
	Main           bool
	ComposeProject string
}

// InstanceTargets lists the TCP ports of one instance as targets, from the
// ports `theo status`, the web interface and MCP report.
func InstanceTargets(in Instance, ports []engine.PortStatus) []Target {
	var out []Target
	for _, port := range ports {
		if port.Protocol != "tcp" || port.HostPort <= 0 {
			continue
		}
		out = append(out, Target{
			Project: in.Project, Worktree: in.Worktree, Branch: in.Branch, Main: in.Main,
			ComposeProject: in.ComposeProject, Service: port.Service,
			ContainerPort: port.ContainerPort, HostIP: urlHost(port.URL), HostPort: port.HostPort,
			Running: port.Status == engine.PortRunning,
		})
	}
	return out
}

// urlHost is the host of a port's URL; the engine writes loopback and
// wildcard addresses as "localhost" and any other address as itself.
func urlHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	h := parsed.Hostname()
	if h == "localhost" {
		return ""
	}
	return strings.Trim(h, "[]")
}

// EngineSource reads the targets of every registered project (or of one)
// from the engine: Git, Theo's records and Docker, the way `theo status`
// does.
func EngineSource(e *engine.Engine, project string) func(context.Context) ([]Target, error) {
	return func(ctx context.Context) ([]Target, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		r, err := e.Status(engine.WithCachedConfig(engine.WithRequestMemo(ctx)), project)
		if err != nil {
			return nil, err
		}
		return FromStatus(r), nil
	}
}

// AddURLs fills the proxy_urls of each instance of a status report with the
// addresses a proxy listening on port serves it on.
func AddURLs(r *engine.StatusReport, port int) {
	tbl := BuildTable(FromStatus(r), port)
	for pi := range r.Projects {
		p := &r.Projects[pi]
		for wi := range p.Worktrees {
			wt := &p.Worktrees[wi]
			if wt.Instance == nil {
				continue
			}
			var urls []string
			for _, rt := range tbl.Routes {
				if rt.Project == p.Name && rt.Worktree == wt.Path {
					urls = append(urls, rt.URL)
				}
			}
			wt.Instance.ProxyURLs = urls
		}
	}
}

// Link is an address the proxy serves a service of an instance on.
type Link struct {
	Service string `json:"service"`
	URL     string `json:"url"`
	Default bool   `json:"default,omitempty"` // also reachable as <instance>.<project>
	Running bool   `json:"running"`
}

// Links returns the proxy addresses of the instances in targets, for a
// proxy on port, grouped by worktree path. The short form comes first, then
// one link per service.
func Links(targets []Target, port int) map[string][]Link {
	out := map[string][]Link{}
	for _, rt := range BuildTable(targets, port).Routes {
		out[rt.Worktree] = append(out[rt.Worktree], Link{Service: rt.Service, URL: rt.URL, Default: rt.Default, Running: rt.Running})
	}
	return out
}

// RunningPort returns the port of the proxy recorded in Theo's data
// directory dir, if one is listening there now.
func RunningPort(dir string) (int, bool) {
	if dir == "" {
		return 0, false
	}
	s, ok := Running(dir)
	if !ok {
		return 0, false
	}
	return s.Port, true
}
