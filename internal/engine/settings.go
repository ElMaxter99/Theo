package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/instances"
)

// SettingsStore persists instance settings and the operation history.
type SettingsStore interface {
	Load() (instances.State, error)
	Save(instances.State) error
	Append(instances.Event) error
	Events() ([]instances.Event, error)
}

// memSettings keeps settings and history in memory, for engines that are not
// given a place to persist them.
type memSettings struct {
	state  instances.State
	events []instances.Event
}

func (m *memSettings) Load() (instances.State, error) {
	out := instances.State{}
	for k, v := range m.state {
		out[k] = v
	}
	return out, nil
}
func (m *memSettings) Save(s instances.State) error       { m.state = s; return nil }
func (m *memSettings) Append(ev instances.Event) error    { m.events = append(m.events, ev); return nil }
func (m *memSettings) Events() ([]instances.Event, error) { return m.events, nil }

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Settings returns the settings Theo keeps for an instance.
func (e *Engine) Settings(inst *Instance) (instances.Config, error) {
	state, err := e.settings.Load()
	if err != nil {
		return instances.Config{}, err
	}
	return state[inst.Workspace], nil
}

func (e *Engine) updateSettings(inst *Instance, change func(*instances.Config) error) error {
	state, err := e.settings.Load()
	if err != nil {
		return err
	}
	cfg := state[inst.Workspace]
	if err := change(&cfg); err != nil {
		return err
	}
	cfg.Project = inst.Project
	if cfg.Empty() {
		delete(state, inst.Workspace)
	} else {
		state[inst.Workspace] = cfg
	}
	return e.settings.Save(state)
}

// SetEnv sets an environment variable for an instance, over what the project
// files and env files say. services limits it to some services; empty means
// every service. The project's files are not modified: the variable is
// applied through a generated Compose file and env file when the instance
// starts.
func (e *Engine) SetEnv(inst *Instance, name, value string, secret bool, services []string) error {
	if !envName.MatchString(name) {
		return fmt.Errorf("invalid variable name %q: use letters, digits and _, not starting with a digit", name)
	}
	for _, s := range services {
		if _, ok := inst.config.Services[s]; !ok {
			return fmt.Errorf("the project has no service %q (services: %s)", s, strings.Join(compose.SortedKeys(inst.config.Services), ", "))
		}
	}
	sort.Strings(services)
	return e.updateSettings(inst, func(c *instances.Config) error {
		if c.Env == nil {
			c.Env = map[string]instances.Var{}
		}
		c.Env[name] = instances.Var{Value: value, Secret: secret, Services: services}
		return nil
	})
}

// UnsetEnv removes an instance's variable. It reports whether it existed.
func (e *Engine) UnsetEnv(inst *Instance, name string) (bool, error) {
	found := false
	err := e.updateSettings(inst, func(c *instances.Config) error {
		_, found = c.Env[name]
		delete(c.Env, name)
		return nil
	})
	return found, err
}

// SetNeutralizeContainerNames sets whether the instance removes fixed
// container_name settings from the services that have one.
func (e *Engine) SetNeutralizeContainerNames(inst *Instance, on bool) error {
	return e.updateSettings(inst, func(c *instances.Config) error {
		c.NeutralizeContainerNames = on
		return nil
	})
}

// FixedContainerNames returns the services that set a container_name. Two
// instances of them cannot run at once, because Docker container names are
// global.
func (inst *Instance) FixedContainerNames() map[string]string {
	out := map[string]string{}
	for name, s := range inst.config.Services {
		if s.ContainerName != "" {
			out[name] = s.ContainerName
		}
	}
	return out
}

// Mask is shown instead of the value of a secret variable.
const Mask = "********"

// MaskSecrets replaces the values of the instance's secret variables in text.
func MaskSecrets(text string, cfg instances.Config) string {
	for _, v := range cfg.Env {
		if v.Secret && v.Value != "" {
			text = strings.ReplaceAll(text, v.Value, Mask)
		}
	}
	return text
}

// ApplyInstance prepares an instance for `up`: it plans the host ports (see
// PlanPorts; with auto, the taken ones are reassigned and remembered) and
// applies the instance's settings. Whatever has to change goes in a Compose
// file and an env file that Theo generates outside the project, validated
// with `docker compose config`, and from then on the instance runs with
// them. Without changes the instance runs with the project's own files.
func (e *Engine) ApplyInstance(ctx context.Context, inst *Instance, profiles []string, auto bool) (*PortPlan, error) {
	plan, state, err := e.planPorts(ctx, inst, profiles, auto)
	if err != nil {
		return nil, err
	}
	settings, err := e.Settings(inst)
	if err != nil {
		return nil, err
	}

	inst.neutralized = settings.NeutralizeContainerNames
	overlay := compose.Overlay{Ports: plan.changed}
	if settings.NeutralizeContainerNames {
		overlay.ResetContainerName = compose.SortedKeys(inst.FixedContainerNames())
	}
	env := map[string]string{}
	if len(settings.Env) > 0 {
		overlay.Environment = map[string]map[string]string{}
		for name, v := range settings.Env {
			env[name] = v.Value
			targets := v.Services
			if len(targets) == 0 {
				targets = compose.SortedKeys(inst.config.Services)
			}
			for _, svc := range targets {
				if _, ok := inst.config.Services[svc]; !ok {
					continue // the service no longer exists
				}
				if overlay.Environment[svc] == nil {
					overlay.Environment[svc] = map[string]string{}
				}
				overlay.Environment[svc][name] = v.Value
			}
		}
	}

	inst.files, inst.instanceEnv, inst.generated = nil, "", nil
	overlayPath, envPath := e.instanceFile(inst, ".yaml"), e.instanceFile(inst, ".env")
	if overlay.Empty() && len(env) == 0 {
		if auto {
			if err := e.portStore.Save(state); err != nil {
				return nil, err
			}
		}
		if e.dataDir != "" { // stale files of settings that were removed
			os.Remove(overlayPath)
			os.Remove(envPath)
		}
		return plan, nil
	}

	if err := e.requireOverlayCompose(ctx); err != nil {
		return nil, err
	}
	base, err := e.baseComposeFiles(ctx, inst)
	if err != nil {
		return nil, err
	}
	if e.dataDir == "" {
		return nil, errors.New("no data directory to keep the generated Compose file")
	}
	if err := os.MkdirAll(filepath.Dir(overlayPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(overlayPath, overlay.Render(), 0o600); err != nil {
		return nil, err
	}
	instanceEnv := ""
	if len(env) > 0 {
		if err := os.WriteFile(envPath, renderEnvFile(env), 0o600); err != nil {
			return nil, err
		}
		instanceEnv = envPath
	} else {
		os.Remove(envPath)
	}

	t := inst.target()
	t.Files = append(base, overlayPath)
	t.EnvFiles = withInstanceEnv(inst.Workspace, inst.envFiles, instanceEnv)
	cfg, err := e.compose.ConfigFor(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("the configuration with this instance's settings is not valid (generated file %s): %s", overlayPath, MaskSecrets(err.Error(), settings))
	}
	if err := checkEffective(plan, cfg); err != nil {
		return nil, fmt.Errorf("%w (generated file %s)", err, overlayPath)
	}
	if err := checkOverlay(overlay, cfg); err != nil {
		return nil, fmt.Errorf("%w (generated file %s)", err, overlayPath)
	}
	if auto {
		if err := e.portStore.Save(state); err != nil {
			return nil, err
		}
	}
	inst.files, inst.instanceEnv, inst.generated = t.Files, instanceEnv, []string{overlayPath}
	if instanceEnv != "" {
		inst.generated = append(inst.generated, instanceEnv)
	}
	return plan, nil
}

// instanceFile is the path of a file Theo generates for an instance.
func (e *Engine) instanceFile(inst *Instance, ext string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(inst.Workspace)))
	return filepath.Join(e.dataDir, "overlays", slug(inst.Compose)+"-"+hex.EncodeToString(sum[:3])+ext)
}

// renderEnvFile renders variables as a Compose env file, one per line, in a
// form that keeps the value exactly as given.
func renderEnvFile(env map[string]string) []byte {
	var b strings.Builder
	for _, k := range compose.SortedKeys(env) {
		v := env[k]
		if strings.ContainsAny(v, "'\n\r") {
			v = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\r", `\r`).Replace(v)
			fmt.Fprintf(&b, "%s=\"%s\"\n", k, v)
		} else {
			fmt.Fprintf(&b, "%s='%s'\n", k, v)
		}
	}
	return []byte(b.String())
}

// ResolvedConfig returns the configuration the instance would run with, as
// `docker compose config` prints it, with secret values masked. It applies
// the instance's generated files, but starts nothing.
func (e *Engine) ResolvedConfig(ctx context.Context, inst *Instance, profiles []string, auto bool) (string, error) {
	if _, err := e.ApplyInstance(ctx, inst, profiles, auto); err != nil {
		return "", err
	}
	out, err := e.compose.ConfigText(ctx, inst.target())
	if err != nil {
		return "", err
	}
	settings, err := e.Settings(inst)
	if err != nil {
		return "", err
	}
	return MaskSecrets(string(out), settings), nil
}

// GeneratedFiles lists the generated files the instance uses, after
// ApplyInstance.
func (inst *Instance) GeneratedFiles() []string { return inst.generated }

func (e *Engine) record(inst *Instance, op string, err error) {
	ev := instances.Event{
		Time: e.now().UTC(), User: currentUser(), Project: inst.Project, Workspace: inst.Workspace,
		Compose: inst.Compose, Operation: op, Result: "ok",
	}
	if err != nil {
		ev.Result, ev.Error = "error", err.Error()
	}
	_ = e.settings.Append(ev) // the history is best effort: it never fails the operation
}

func currentUser() string {
	for _, k := range []string{"USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// History returns the operations Theo ran on the instances of a project,
// newest last. With a worktree it is limited to that worktree's instance.
// limit 0 means all.
func (e *Engine) History(ctx context.Context, project, worktree string, limit int) ([]instances.Event, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	var path string
	if worktree != "" {
		list, err := e.git.Worktrees(ctx, p.Path)
		if err != nil {
			return nil, err
		}
		wt, err := selectWorktree(list, worktree)
		if err != nil {
			return nil, fmt.Errorf("project %q: %w", p.Name, err)
		}
		path = wt.Path
	}
	all, err := e.settings.Events()
	if err != nil {
		return nil, err
	}
	var out []instances.Event
	for _, ev := range all {
		if !strings.EqualFold(ev.Project, p.Name) || (path != "" && !samePath(ev.Workspace, path)) {
			continue
		}
		out = append(out, ev)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
