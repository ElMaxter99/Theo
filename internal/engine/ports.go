package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/ports"
)

// PortStore persists the host ports assigned to instances.
type PortStore interface {
	Load() (ports.State, error)
	Save(ports.State) error
}

// Port statuses.
const (
	PortFree       = "free"       // nothing listens on it
	PortRunning    = "running"    // this instance's own containers publish it
	PortInUse      = "in use"     // something else listens on it
	PortReassigned = "reassigned" // auto ports: the configured port was taken, another one was chosen
	PortKept       = "kept"       // auto ports: the port assigned earlier is still available
	PortRandom     = "random"     // no fixed host port: Docker picks one at start
	PortRange      = "range"      // a range of host ports; Theo leaves it alone
	maxPortSearch  = 1000         // candidates tried after the configured port
)

// PortMapping is a port a service publishes on the host and what Theo
// decided about it.
type PortMapping struct {
	Service    string `json:"service"`
	Target     uint32 `json:"target"`
	Protocol   string `json:"protocol"`
	HostIP     string `json:"host_ip,omitempty"`
	Configured string `json:"configured,omitempty"` // published port in the Compose configuration
	Effective  int    `json:"effective,omitempty"`  // host port the instance will use; 0 if not fixed
	Running    int    `json:"running,omitempty"`    // host port the running container publishes; 0 if none
	Status     string `json:"status"`
}

// Conflict reports whether the mapping asks for a host port that is taken.
func (m PortMapping) Conflict() bool { return m.Status == PortInUse }

// URL is where a TCP service can be reached from the host, or "" when Theo
// cannot tell (UDP, no fixed port).
func (m PortMapping) URL() string {
	port := m.Effective
	if m.Running != 0 {
		port = m.Running
	}
	if port == 0 || m.Protocol != "tcp" {
		return ""
	}
	host := m.HostIP
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}

// PortPlan is the result of PlanPorts.
type PortPlan struct {
	Mappings []PortMapping `json:"ports"`
	Overlay  string        `json:"overlay,omitempty"` // generated Compose file for the reassigned ports

	changed map[string][]compose.Port // services whose ports are rewritten, with their complete list
}

// Conflicts returns the mappings that ask for a port that is taken.
func (p *PortPlan) Conflicts() []PortMapping {
	var out []PortMapping
	for _, m := range p.Mappings {
		if m.Conflict() {
			out = append(out, m)
		}
	}
	return out
}

var rangeRe = regexp.MustCompile(`^\d+-\d+$`)

// PlanPorts looks at the ports the instance publishes on the host, for the
// services that start with the given profiles (services without profiles
// always do). It never changes anything.
//
// Without auto, a mapping whose configured port is taken is reported as
// in use. With auto, Theo chooses another free port for it, starting right
// after the configured one, and prefers the port it assigned to the instance
// last time so that the address stays the same between restarts. Ports
// reserved for other instances count as taken.
func (e *Engine) PlanPorts(ctx context.Context, inst *Instance, profiles []string, auto bool) (*PortPlan, error) {
	plan, _, err := e.planPorts(ctx, inst, profiles, auto)
	return plan, err
}

func (e *Engine) planPorts(ctx context.Context, inst *Instance, profiles []string, auto bool) (*PortPlan, ports.State, error) {
	state, err := e.portStore.Load()
	if err != nil {
		return nil, nil, err
	}
	saved := state[inst.Workspace]

	// Host ports published by this instance's own containers are not
	// conflicts. Without a Docker daemon there are none to find.
	own := map[string]int{}
	running := map[string]int{}
	if cs, err := e.compose.PS(ctx, inst.target()); err == nil {
		for _, c := range cs {
			for _, p := range c.Publishers {
				if p.PublishedPort == 0 {
					continue
				}
				proto := orTCP(p.Protocol)
				own[portID(p.PublishedPort, proto)] = p.PublishedPort
				running[ports.Key(c.Service, uint32(p.TargetPort), proto)] = p.PublishedPort
			}
		}
	}

	reserved := map[string]bool{}
	for ws, assigned := range state {
		if ws == inst.Workspace {
			continue
		}
		for key, port := range assigned {
			reserved[portID(port, protoOf(key))] = true
		}
	}

	plan := &PortPlan{Mappings: []PortMapping{}}
	used := map[string]bool{}
	next := ports.State{}
	changed := map[string][]compose.Port{} // services whose ports are rewritten

	available := func(hostIP string, port int, proto string) bool {
		id := portID(port, proto)
		if used[id] {
			return false
		}
		if _, ok := own[id]; ok {
			return true
		}
		return !reserved[id] && e.portFree(hostIP, port, proto)
	}

	for _, name := range compose.SortedKeys(inst.config.Services) {
		svc := inst.config.Services[name]
		if !startsWith(svc, profiles) {
			continue
		}
		effective := make([]compose.Port, len(svc.Ports))
		copy(effective, svc.Ports)
		rewrite := false
		for i, p := range svc.Ports {
			proto := orTCP(p.Protocol)
			m := PortMapping{Service: name, Target: p.Target, Protocol: proto, HostIP: p.HostIP, Configured: p.Published}
			key := ports.Key(name, p.Target, proto)
			m.Running = running[key]
			configured, err := strconv.Atoi(p.Published)
			switch {
			case p.Published == "":
				m.Status = PortRandom
			case err != nil || configured <= 0 || rangeRe.MatchString(p.Published):
				m.Status = PortRange
			default:
				desired := configured
				if s, ok := saved[key]; auto && ok {
					desired = s
				}
				switch {
				case available(p.HostIP, desired, proto):
					m.Effective = desired
					m.Status = PortFree
					if _, ours := own[portID(desired, proto)]; ours {
						m.Status = PortRunning
					}
					if desired != configured {
						m.Status = PortKept
					}
				case auto:
					m.Effective = e.nextFree(p.HostIP, configured, proto, available)
					if m.Effective == 0 {
						return nil, nil, fmt.Errorf("no free host port found after %d for %s (container port %d/%s)", configured, name, p.Target, proto)
					}
					m.Status = PortReassigned
				default:
					m.Effective = configured
					m.Status = PortInUse
				}
				used[portID(m.Effective, proto)] = true
				if auto && m.Effective != configured {
					if next[inst.Workspace] == nil {
						next[inst.Workspace] = map[string]int{}
					}
					next[inst.Workspace][key] = m.Effective
					effective[i].Published = strconv.Itoa(m.Effective)
					rewrite = true
				}
			}
			plan.Mappings = append(plan.Mappings, m)
		}
		if rewrite {
			changed[name] = effective
		}
	}

	// Keep the assignments of the other instances; replace this one's.
	out := ports.State{}
	for ws, assigned := range state {
		if ws != inst.Workspace {
			out[ws] = assigned
		}
	}
	if auto && next[inst.Workspace] != nil {
		out[inst.Workspace] = next[inst.Workspace]
	} else if !auto && saved != nil {
		out[inst.Workspace] = saved
	}
	if len(changed) > 0 {
		plan.changed = changed
		plan.Overlay = string(compose.Overlay{Ports: changed}.Render())
	}
	return plan, out, nil
}

// checkEffective verifies that the resolved configuration publishes the
// ports of the plan.
func checkEffective(plan *PortPlan, cfg *compose.Project) error {
	for _, m := range plan.Mappings {
		if m.Effective == 0 {
			continue
		}
		found := false
		for _, p := range cfg.Services[m.Service].Ports {
			if p.Target == m.Target && orTCP(p.Protocol) == m.Protocol && p.Published == strconv.Itoa(m.Effective) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("the resolved configuration does not publish %s on host port %d", m.Service, m.Effective)
		}
	}
	return nil
}

var envComposeFile = regexp.MustCompile(`(?m)^\s*(?:export\s+)?COMPOSE_FILE\s*=`)

// baseComposeFiles returns the Compose files Compose would load for the
// instance, which --auto-ports must name explicitly to add its own.
func (e *Engine) baseComposeFiles(inst *Instance) ([]string, error) {
	if os.Getenv("COMPOSE_FILE") != "" {
		return nil, errors.New("--auto-ports does not support COMPOSE_FILE yet; unset it or free the ports by hand")
	}
	for _, f := range inst.envFiles {
		if data, err := os.ReadFile(f); err == nil && envComposeFile.Match(data) {
			return nil, fmt.Errorf("--auto-ports does not support COMPOSE_FILE (set in %s) yet", f)
		}
	}
	files := e.baseFiles(inst.Workspace)
	if len(files) == 0 {
		return nil, fmt.Errorf("--auto-ports needs a compose.yaml (or docker-compose.yml) in %s", inst.Workspace)
	}
	return files, nil
}

// nextFree returns the first available port after from, or 0.
func (e *Engine) nextFree(hostIP string, from int, proto string, available func(string, int, string) bool) int {
	for p := from + 1; p <= from+maxPortSearch && p <= 65535; p++ {
		if available(hostIP, p, proto) {
			return p
		}
	}
	return 0
}

// startsWith reports whether a service starts when the given profiles are
// enabled: it has no profile, or one of them is enabled.
func startsWith(s compose.Service, enabled []string) bool {
	if len(s.Profiles) == 0 {
		return true
	}
	for _, p := range s.Profiles {
		for _, e := range enabled {
			if p == e || e == "*" {
				return true
			}
		}
	}
	return false
}

func orTCP(proto string) string {
	if proto == "" {
		return "tcp"
	}
	return proto
}

func portID(port int, proto string) string { return fmt.Sprintf("%d/%s", port, proto) }

// protoOf extracts the protocol from a ports.Key.
func protoOf(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return "tcp"
}
