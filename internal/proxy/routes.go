// Package proxy is `theo proxy`: a local HTTP reverse proxy that serves each
// instance under a name of its own, <service>.<instance>.<project>.localhost,
// instead of a host port that changes with --auto-ports.
//
// This file holds the routing table, which is pure: it turns what Theo knows
// about the instances (Target) into host names (Route) and looks a Host
// header up. server.go serves it; source.go reads the targets from the
// engine.
package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// Suffix is the domain every route lives under. Browsers resolve any name
// under it to the loopback address by themselves (see docs/cli/proxy.md).
const Suffix = "localhost"

// DefaultPort is the port `theo proxy` listens on by default.
const DefaultPort = 7080

// MainInstance is the instance name of a project's main worktree.
const MainInstance = "main"

// preferredDefaults are the services the short form <instance>.<project>
// goes to, in this order, when the instance publishes one of them.
var preferredDefaults = []string{"web", "app", "api", "frontend"}

// Target is one TCP port a service of an instance publishes on the host.
type Target struct {
	Project        string // registered project name
	Worktree       string // worktree path
	Branch         string // empty when detached
	Main           bool   // the project's main worktree
	ComposeProject string // empty when the configuration could not be read
	Service        string
	ContainerPort  uint32
	HostIP         string // where the port is published; "" or a wildcard means loopback
	HostPort       int
	Running        bool // the instance's own container publishes it now
}

// Route is a host name the proxy answers, and where it sends the request.
type Route struct {
	Host           string   `json:"host"` // web.main.shop.localhost
	URL            string   `json:"url"`  // http://web.main.shop.localhost:7080
	Default        bool     `json:"default"`
	Project        string   `json:"project"`
	Instance       string   `json:"instance"`
	Aliases        []string `json:"aliases,omitempty"` // other instance names that reach it (the branch)
	Service        string   `json:"service"`
	Worktree       string   `json:"worktree"`
	Branch         string   `json:"branch,omitempty"`
	ComposeProject string   `json:"compose_project,omitempty"`
	ContainerPort  uint32   `json:"container_port"`
	HostPort       int      `json:"host_port"`
	Target         string   `json:"target"` // 127.0.0.1:8080
	Running        bool     `json:"running"`
}

// Table is the set of routes, indexed by host name.
type Table struct {
	Routes []Route // sorted: project, instance, then the short form before the services
	byHost map[string]int
}

// HostLabel turns a name into a DNS label with Theo's slug rule (engine.Slug)
// plus what host names need on top: no '_' and at most 63 characters.
func HostLabel(s string) string {
	l := strings.ReplaceAll(engine.Slug(s), "_", "-")
	if len(l) > 63 {
		l = strings.TrimRight(l[:63], "-")
	}
	return l
}

// instanceLabel names the instance of a target in host names: "main" for the
// main worktree, otherwise what Theo appended to the project name to make
// the Compose project name (the worktree directory, plus a short hash when
// two worktrees share a directory name).
func instanceLabel(t Target) string {
	if t.Main {
		return MainInstance
	}
	prefix := engine.Slug(t.Project) + "-"
	if rest, ok := strings.CutPrefix(t.ComposeProject, prefix); ok && rest != "" {
		return HostLabel(rest)
	}
	return HostLabel(filepath.Base(t.Worktree))
}

type instanceKey struct{ project, worktree string }

type instanceInfo struct {
	project, label string
	aliases        []string
	targets        []Target
}

// BuildTable makes the routing table of targets. port is the proxy's port,
// written into the URLs (80 leaves it out).
func BuildTable(targets []Target, port int) *Table {
	// Group by instance; keep only TCP ports with a known host port.
	byInst := map[instanceKey]*instanceInfo{}
	var order []instanceKey
	for _, t := range targets {
		if t.HostPort <= 0 || t.Service == "" {
			continue
		}
		k := instanceKey{t.Project, t.Worktree}
		in := byInst[k]
		if in == nil {
			in = &instanceInfo{project: HostLabel(t.Project), label: instanceLabel(t)}
			byInst[k] = in
			order = append(order, k)
		}
		in.targets = append(in.targets, t)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := byInst[order[i]], byInst[order[j]]
		if a.project != b.project {
			return a.project < b.project
		}
		am, bm := a.targets[0].Main, b.targets[0].Main
		if am != bm {
			return am // the main worktree keeps "main" when a linked one is also named so
		}
		return order[i].worktree < order[j].worktree
	})

	// Instance names must be unique within a project; a clash (two projects
	// slugging to the same label, a linked worktree named "main") gets a
	// short hash of the worktree path.
	used := map[string]bool{}
	for _, k := range order {
		in := byInst[k]
		key := in.project + "/" + in.label
		if used[key] {
			sum := sha256.Sum256([]byte(filepath.Clean(k.worktree)))
			in.label = HostLabel(in.label + "-" + hex.EncodeToString(sum[:3]))
			key = in.project + "/" + in.label
		}
		used[key] = true
	}
	// The branch is an alias when it names no other instance.
	for _, k := range order {
		in := byInst[k]
		if b := in.targets[0].Branch; b != "" {
			if a := HostLabel(b); a != in.label && !used[in.project+"/"+a] {
				in.aliases = append(in.aliases, a)
				used[in.project+"/"+a] = true
			}
		}
	}

	tbl := &Table{Routes: []Route{}, byHost: map[string]int{}}
	add := func(r Route, names ...string) {
		tbl.Routes = append(tbl.Routes, r)
		for _, n := range names {
			if _, dup := tbl.byHost[n]; !dup {
				tbl.byHost[n] = len(tbl.Routes) - 1
			}
		}
	}
	for _, k := range order {
		in := byInst[k]
		services := pickPorts(in.targets)
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		sort.Strings(names)
		def := defaultService(names)
		instNames := append([]string{in.label}, in.aliases...)
		route := func(t Target, isDefault bool) Route {
			host := HostLabel(t.Service) + "." + in.label + "." + in.project + "." + Suffix
			if isDefault {
				host = in.label + "." + in.project + "." + Suffix
			}
			return Route{
				Host: host, URL: hostURL(host, port), Default: isDefault,
				Project: t.Project, Instance: in.label, Aliases: in.aliases, Service: t.Service,
				Worktree: t.Worktree, Branch: t.Branch, ComposeProject: t.ComposeProject,
				ContainerPort: t.ContainerPort, HostPort: t.HostPort, Target: dialAddr(t), Running: t.Running,
			}
		}
		if def != "" {
			var hosts []string
			for _, n := range instNames {
				hosts = append(hosts, n+"."+in.project+"."+Suffix)
			}
			add(route(services[def], true), hosts...)
		}
		for _, name := range names {
			var hosts []string
			for _, n := range instNames {
				hosts = append(hosts, HostLabel(name)+"."+n+"."+in.project+"."+Suffix)
			}
			add(route(services[name], false), hosts...)
		}
	}
	return tbl
}

// pickPorts chooses, for each service, the port its route goes to: the
// lowest published container port (80 before 443, 8080 before 9229).
func pickPorts(targets []Target) map[string]Target {
	out := map[string]Target{}
	for _, t := range targets {
		cur, ok := out[t.Service]
		if !ok || t.ContainerPort < cur.ContainerPort || t.ContainerPort == cur.ContainerPort && t.HostPort < cur.HostPort {
			out[t.Service] = t
		}
	}
	return out
}

// defaultService is the service of the short form: web, app, api or
// frontend, in that order, else the first service by name. names is sorted.
func defaultService(names []string) string {
	for _, want := range preferredDefaults {
		for _, n := range names {
			if n == want {
				return n
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// dialAddr is where the proxy connects for t: the address it is published
// on, or the loopback address when it is published on every interface.
func dialAddr(t Target) string {
	host := t.HostIP
	switch host {
	case "", "0.0.0.0", "::", "localhost":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(t.HostPort))
}

func hostURL(host string, port int) string {
	if port == 80 || port == 0 {
		return "http://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// Lookup returns the route of a Host header (any letter case, with or
// without a port or a trailing dot).
func (t *Table) Lookup(host string) (*Route, bool) {
	name := NormalizeHost(host)
	i, ok := t.byHost[name]
	if !ok {
		return nil, false
	}
	return &t.Routes[i], true
}

// NormalizeHost strips the port and trailing dot of a Host header and
// lowercases it.
func NormalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
}

// IsRouteShaped reports whether host looks like one of the proxy's names
// (<instance>.<project>.localhost or <service>.<instance>.<project>.localhost),
// whether or not it exists.
func IsRouteShaped(host string) bool {
	name := NormalizeHost(host)
	rest, ok := strings.CutSuffix(name, "."+Suffix)
	if !ok {
		return false
	}
	n := len(strings.Split(rest, "."))
	return n == 2 || n == 3
}

// Hosts returns every host name of the table, aliases included, sorted.
func (t *Table) Hosts() []string {
	out := make([]string, 0, len(t.byHost))
	for h := range t.byHost {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Project returns the routes of one project (by registered name, any case).
func (t *Table) Project(name string) []Route {
	out := []Route{}
	for _, r := range t.Routes {
		if strings.EqualFold(r.Project, name) {
			out = append(out, r)
		}
	}
	return out
}
