package repoconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Kinds of start.
const (
	RunCompose = "compose" // Docker Compose, which Theo already manages
	RunScript  = "script"  // one process started in the worktree
	RunStatic  = "static"  // a build that Theo serves itself, under a base path
)

// DefaultReadyTimeout is how long Theo waits for the application to answer.
const DefaultReadyTimeout = 90

// RunSpec is the "run" section of .theo.json: how the application of this
// repository is started and how Theo knows it is ready.
//
//	"run": {
//	  "kind": "script",
//	  "install": ["npm ci"],
//	  "start": "npm run dev -- --port ${PORT}",
//	  "port": {"env": "PORT", "auto": true},
//	  "env": {"API_URL": "http://localhost:4000"},
//	  "ready": {"path": "/", "status": [200, 304], "timeout_s": 90}
//	}
type RunSpec struct {
	// Kind is "compose" or "script"; empty means "script" when Start is set
	// and "compose" otherwise.
	Kind string `json:"kind,omitempty"`
	// Install lists commands run once before starting (like after_create).
	Install []string `json:"install,omitempty"`
	// Start is the command that runs the application, in the worktree. It
	// must keep running. ${PORT} (or the variable named in Port.Env) is the
	// port Theo gives it.
	Start string `json:"start,omitempty"`
	// Service names the Compose service that answers on the web (compose).
	Service string   `json:"service,omitempty"`
	Port    *RunPort `json:"port,omitempty"`
	// Env is extra environment for the start command. Values may use ${PORT}.
	Env map[string]string `json:"env,omitempty"`

	// The next fields are for kind "static": an application that only
	// generates a folder of files. Theo runs Install and Build in the
	// worktree and serves Dir itself, under Base.
	Build []string `json:"build,omitempty"`
	// Dir is the folder the build leaves, relative to the worktree.
	Dir string `json:"dir,omitempty"`
	// Base is the path the application lives under ("/portal"; "/" for the
	// root). Every instance keeps it: each is served on its own port.
	Base string `json:"base,omitempty"`
	// SPA answers a path without a file with index.html (HTML5 routes).
	SPA bool `json:"spa,omitempty"`
	// Proxy sends paths under a prefix to a server that already exists (the
	// API), keeping the path: "/portal/api" to "http://localhost:4000".
	Proxy map[string]string `json:"proxy,omitempty"`

	// Ready says when the application counts as up (every kind).
	Ready *RunReady `json:"ready,omitempty"`
}

// RunPort says how the port reaches the application.
type RunPort struct {
	// Env is the variable that carries the port (default PORT).
	Env string `json:"env,omitempty"`
	// Auto asks Theo for a free port (default true), so two branches do not clash.
	Auto *bool `json:"auto,omitempty"`
	// Fixed is the port to use when Auto is false.
	Fixed int `json:"fixed,omitempty"`
}

// RunReady is how Theo decides the application is up.
type RunReady struct {
	Path     string `json:"path,omitempty"`      // default "/"
	Status   []int  `json:"status,omitempty"`    // default 200 and 304
	TimeoutS int    `json:"timeout_s,omitempty"` // default DefaultReadyTimeout
}

// EffectiveKind resolves an empty kind.
func (r RunSpec) EffectiveKind() string {
	if r.Kind != "" {
		return r.Kind
	}
	if strings.TrimSpace(r.Start) != "" {
		return RunScript
	}
	if r.Dir != "" || len(r.Build) > 0 {
		return RunStatic
	}
	return RunCompose
}

// PortEnv is the variable that carries the port.
func (r RunSpec) PortEnv() string {
	if r.Port != nil && r.Port.Env != "" {
		return r.Port.Env
	}
	return "PORT"
}

// AutoPort reports whether Theo picks the port.
func (r RunSpec) AutoPort() bool {
	return r.Port == nil || r.Port.Auto == nil || *r.Port.Auto
}

// ReadyPath, ReadyStatus and ReadyTimeout apply the defaults.
func (r RunSpec) ReadyPath() string {
	if r.Ready != nil && r.Ready.Path != "" {
		return r.Ready.Path
	}
	if r.EffectiveKind() == RunStatic && r.Base != "" && r.Base != "/" {
		return r.Base + "/"
	}
	return "/"
}

func (r RunSpec) ReadyStatus() []int {
	if r.Ready != nil && len(r.Ready.Status) > 0 {
		return r.Ready.Status
	}
	return []int{200, 304}
}

func (r RunSpec) ReadyTimeout() int {
	if r.Ready != nil && r.Ready.TimeoutS > 0 {
		return r.Ready.TimeoutS
	}
	return DefaultReadyTimeout
}

// Validate checks the section. A mistake is an error, not silently ignored.
func (r RunSpec) Validate() error {
	switch r.Kind {
	case "", RunCompose, RunScript, RunStatic:
	default:
		return fmt.Errorf("kind %q is not known (use %q, %q or %q)", r.Kind, RunCompose, RunScript, RunStatic)
	}
	if r.EffectiveKind() == RunScript && strings.TrimSpace(r.Start) == "" {
		return errors.New(`kind "script" needs "start"`)
	}
	if r.EffectiveKind() == RunCompose && strings.TrimSpace(r.Start) != "" {
		return errors.New(`"start" belongs to kind "script"; Compose starts through "theo up"`)
	}
	if r.EffectiveKind() == RunStatic {
		if strings.TrimSpace(r.Start) != "" {
			return errors.New(`kind "static" has no "start": Theo serves the folder itself`)
		}
		if strings.TrimSpace(r.Dir) == "" && len(r.Build) == 0 || !strings.HasPrefix(r.Base, "/") {
			return errors.New(`kind "static" needs "build" or "dir", and "base" starting with /`)
		}
		if strings.HasPrefix(r.Base, "//") || strings.Contains(r.Base, `\`) {
			return fmt.Errorf("base %q must be a path starting with a single /", r.Base)
		}
		if strings.TrimSpace(r.Dir) == "" {
			return errors.New(`kind "static" needs "dir", the folder the build leaves`)
		}
		if filepath.IsAbs(r.Dir) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(r.Dir)), "..") {
			return fmt.Errorf("dir %q must be a folder inside the worktree", r.Dir)
		}
		for _, c := range r.Build {
			if strings.TrimSpace(c) == "" {
				return errors.New(`"build" has an empty command`)
			}
		}
		for prefix, target := range r.Proxy {
			if !strings.HasPrefix(prefix, "/") {
				return fmt.Errorf("proxy prefix %q must start with /", prefix)
			}
			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				return fmt.Errorf("proxy target %q must be an http:// or https:// address", target)
			}
		}
	} else if r.Dir != "" || len(r.Build) > 0 || r.Base != "" || r.SPA || len(r.Proxy) > 0 {
		return errors.New(`"build", "dir", "base", "spa" and "proxy" belong to kind "static"`)
	}
	for _, c := range r.Install {
		if strings.TrimSpace(c) == "" {
			return errors.New(`"install" has an empty command`)
		}
	}
	if r.Port != nil {
		if r.Port.Fixed < 0 || r.Port.Fixed > 65535 {
			return fmt.Errorf("port.fixed %d is not a port", r.Port.Fixed)
		}
		if !r.AutoPort() && r.Port.Fixed == 0 {
			return errors.New(`port.auto is false: set "port.fixed"`)
		}
	}
	if r.Ready != nil {
		if p := r.Ready.Path; p != "" && !strings.HasPrefix(p, "/") {
			return fmt.Errorf("ready.path %q must start with /", p)
		}
		for _, s := range r.Ready.Status {
			if s < 100 || s > 599 {
				return fmt.Errorf("ready.status %d is not an HTTP status", s)
			}
		}
		if r.Ready.TimeoutS < 0 || r.Ready.TimeoutS > 3600 {
			return fmt.Errorf("ready.timeout_s %d is out of range (0 to 3600)", r.Ready.TimeoutS)
		}
	}
	for k := range r.Env {
		if k == "" || strings.ContainsAny(k, "= \t") {
			return fmt.Errorf("env name %q is not valid", k)
		}
	}
	return nil
}

func (r RunSpec) normalized() *RunSpec {
	r.Install = trimAll(r.Install)
	r.Build = trimAll(r.Build)
	if r.Base != "" && r.Base != "/" {
		r.Base = strings.TrimRight(r.Base, "/")
	}
	r.Start = strings.TrimSpace(r.Start)
	r.Kind = r.EffectiveKind()
	return &r
}

// Commands lists every command this section makes Theo run: they run code
// from the repository, so they need the person's approval like after_create.
func (r RunSpec) Commands() []string {
	var out []string
	out = append(out, r.Install...)
	out = append(out, r.Build...)
	if r.Start != "" {
		out = append(out, r.Start)
	}
	return out
}

// RunFingerprint identifies what the section runs (commands and environment).
// Theo remembers the one the person approved; a change asks again.
func RunFingerprint(r RunSpec) string {
	parts := r.Commands()
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, "env:"+k+"="+r.Env[k])
	}
	return Fingerprint(parts)
}
