package engine

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// WarnURLInData is the kind of the warning given when an application that
// stores its own address in its data answers on another address than the
// one its data was made for (Q-020). Theo only warns: it never changes data.
const WarnURLInData = "url-in-data"

// URLInData modes of a project (registry.Project.URLInData).
const (
	URLInDataAuto = "auto"
	URLInDataOn   = "on"
	URLInDataOff  = "off"
)

// urlInDataImages are images known to store their own address in their data.
var urlInDataImages = map[string]string{
	"wordpress": "WordPress", "ghost": "Ghost", "drupal": "Drupal", "joomla": "Joomla",
	"matomo": "Matomo", "nextcloud": "Nextcloud",
}

// imageApp returns the application name of a known image ("WordPress" for
// "wordpress:6", "docker.io/library/wordpress" or "bitnami/wordpress").
func imageApp(image string) string {
	name := image
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return urlInDataImages[strings.ToLower(path.Base(name))]
}

// URLInDataService returns the service of inst whose application stores its
// address in its data, and that application's name ("" when unknown), or
// ok=false when the project does not need the warning. With the mode "on"
// and no known image, it is the service the proxy's short name would use.
func (e *Engine) URLInDataService(inst *Instance) (service, app string, ok bool) {
	if inst == nil || inst.config == nil {
		return "", "", false
	}
	p, err := e.Project(inst.Project)
	if err != nil {
		return "", "", false
	}
	mode := p.URLInData
	if mode == URLInDataOff {
		return "", "", false
	}
	names := make([]string, 0, len(inst.config.Services))
	for name := range inst.config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if a := imageApp(inst.config.Services[name].Image); a != "" {
			return name, a, true
		}
	}
	if mode != URLInDataOn {
		return "", "", false
	}
	for _, pref := range []string{"web", "app", "api", "frontend"} {
		if _, has := inst.config.Services[pref]; has {
			return pref, "", true
		}
	}
	for _, name := range names {
		if len(inst.config.Services[name].Ports) > 0 {
			return name, "", true
		}
	}
	return "", "", false
}

// SetURLInData sets the project's URLInData mode: auto, on or off.
func (e *Engine) SetURLInData(name, mode string) (registry.Project, error) {
	switch mode {
	case URLInDataAuto, URLInDataOn, URLInDataOff:
	default:
		return registry.Project{}, fmt.Errorf("--url-in-data takes auto, on or off, not %q", mode)
	}
	var out registry.Project
	err := e.updateProject(name, func(p *registry.Project) error {
		p.URLInData = mode
		if mode == URLInDataAuto {
			p.URLInData = ""
		}
		out = *p
		return nil
	})
	return out, err
}

// serviceURL is the address of service: its lowest published TCP port, as
// localhost. "" when it publishes none Theo can tell.
func serviceURL(plan *PortPlan, service string) string {
	best, url := uint32(0), ""
	for _, m := range plan.Mappings {
		if m.Service != service || m.Protocol != "tcp" {
			continue
		}
		if u := m.URL(); u != "" && (url == "" || m.Target < best) {
			best, url = m.Target, u
		}
	}
	return url
}

// AddressOf returns the current address of inst's url-in-data service, or
// "" when there is none.
func (e *Engine) AddressOf(ctx context.Context, inst *Instance) string {
	svc, _, ok := e.URLInDataService(inst)
	if !ok {
		return ""
	}
	plan, err := e.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return ""
	}
	return serviceURL(plan, svc)
}

// rememberDataAddress records url as the address of inst's data. Without
// force it only records one when none is recorded yet.
func (e *Engine) rememberDataAddress(inst *Instance, url, source, from string, force bool) {
	if url == "" {
		return
	}
	_ = e.updateSettings(inst, func(c *instances.Config) error {
		if c.DataAddress != nil && !force {
			return nil
		}
		c.DataAddress = &instances.DataAddress{URL: url, Source: source, From: from, Since: e.now().UTC()}
		return nil
	})
}

// ConfirmDataAddress records that the data of inst now holds its current
// address, so the url-in-data warning goes away until the address changes.
func (e *Engine) ConfirmDataAddress(ctx context.Context, inst *Instance) (string, error) {
	if _, _, ok := e.URLInDataService(inst); !ok {
		return "", fmt.Errorf("%s does not store its address in its data (see `theo project set %s --url-in-data on`)", inst.Compose, inst.Project)
	}
	url := e.AddressOf(ctx, inst)
	if url == "" {
		return "", fmt.Errorf("%s publishes no host port Theo can tell, so there is no address to record", inst.Compose)
	}
	e.rememberDataAddress(inst, url, "confirmed", "", true)
	return url, nil
}

// URLInDataWarning compares the address recorded for inst's data with the
// current one (taken from plan) and returns the warning when they differ.
func (e *Engine) URLInDataWarning(inst *Instance, plan *PortPlan, selector string) *StatusWarning {
	svc, app, ok := e.URLInDataService(inst)
	if !ok || plan == nil {
		return nil
	}
	cfg, err := e.Settings(inst)
	if err != nil || cfg.DataAddress == nil {
		return nil
	}
	now := serviceURL(plan, svc)
	if now == "" || now == cfg.DataAddress.URL {
		return nil
	}
	return urlInDataWarning(inst, svc, app, cfg.DataAddress.URL, now, selector)
}

func urlInDataWarning(inst *Instance, svc, app, before, now, selector string) *StatusWarning {
	who := svc
	if app != "" {
		who = app
	}
	w := &StatusWarning{
		Kind: WarnURLInData,
		Message: fmt.Sprintf("%s stores its own address in its data: the data was made for %s, but %s answers on %s, so links and redirects may go to the old address. Theo does not change data",
			who, before, svc, now),
		Fix:      fmt.Sprintf("theo instance set %s -w %s --data-address-updated", inst.Project, selector),
		Previous: before,
		Current:  now,
		Service:  svc,
	}
	if app == "WordPress" {
		w.Command = fmt.Sprintf("wp search-replace '%s' '%s'", before, now)
	}
	return w
}

// urlInDataAfterClone records the source's address as the address of the
// target's data and returns the warning when the target answers elsewhere.
func (e *Engine) urlInDataAfterClone(ctx context.Context, src, dst *Instance) string {
	svc, app, ok := e.URLInDataService(dst)
	if !ok {
		return ""
	}
	before := e.AddressOf(ctx, src)
	if before == "" {
		return ""
	}
	e.rememberDataAddress(dst, before, "clone", src.Compose, true)
	plan, err := e.PlanPorts(ctx, dst, nil, false)
	if err != nil {
		return ""
	}
	now := serviceURL(plan, svc)
	if now == "" || now == before {
		return ""
	}
	w := urlInDataWarning(dst, svc, app, before, now, instanceSelector(dst))
	msg := w.Message
	if w.Command != "" {
		msg += ". With WP-CLI: " + w.Command
	}
	return msg
}

// instanceSelector names inst's worktree for a suggested command.
func instanceSelector(inst *Instance) string {
	if inst.Branch != "" {
		return inst.Branch
	}
	return inst.Workspace
}

// URLInDataCheck is URLInDataWarning with the instance's current ports.
func (e *Engine) URLInDataCheck(ctx context.Context, inst *Instance) *StatusWarning {
	if _, _, ok := e.URLInDataService(inst); !ok {
		return nil
	}
	plan, err := e.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return nil
	}
	return e.URLInDataWarning(inst, plan, instanceSelector(inst))
}
