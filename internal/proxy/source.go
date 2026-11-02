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
			for _, port := range wt.Instance.Ports {
				if port.Protocol != "tcp" || port.HostPort <= 0 {
					continue
				}
				out = append(out, Target{
					Project: p.Name, Worktree: wt.Path, Branch: wt.Branch, Main: wt.Kind == engine.KindMain,
					ComposeProject: wt.Instance.ComposeProject, Service: port.Service,
					ContainerPort: port.ContainerPort, HostIP: urlHost(port.URL), HostPort: port.HostPort,
					Running: port.Status == engine.PortRunning,
				})
			}
		}
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
