package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
)

// This file holds what keeps a request that looks at many projects from
// asking Git and Docker for the same thing again and again: a per-request
// memo of `git worktree list`, one read of all Compose containers shared by
// every instance, and an opt-in cache of `docker compose config`.

// --- per-request memo of `git worktree list` ---

type memoKey struct{}

type requestMemo struct {
	mu    sync.Mutex
	lists map[string]*memoEntry
}

type memoEntry struct {
	once sync.Once
	list []git.Worktree
	err  error
}

// WithRequestMemo returns a context under which each `git worktree list` runs
// at most once per directory. Use it for one request or one polling cycle
// (never keep it around: nothing in it expires), so that resolving the
// instances of many projects, or Reconcile and the checks it runs, share the
// list instead of asking Git once per step.
func WithRequestMemo(ctx context.Context) context.Context {
	if _, ok := ctx.Value(memoKey{}).(*requestMemo); ok {
		return ctx
	}
	return context.WithValue(ctx, memoKey{}, &requestMemo{lists: map[string]*memoEntry{}})
}

// gitWorktrees is e.git.Worktrees, memoized when ctx carries a memo. The
// returned slice is the caller's own.
func (e *Engine) gitWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	m, _ := ctx.Value(memoKey{}).(*requestMemo)
	if m == nil {
		return e.git.Worktrees(ctx, dir)
	}
	key := filepath.Clean(dir)
	m.mu.Lock()
	ent := m.lists[key]
	if ent == nil {
		ent = &memoEntry{}
		m.lists[key] = ent
	}
	m.mu.Unlock()
	ent.once.Do(func() { ent.list, ent.err = e.git.Worktrees(ctx, dir) })
	if ent.err != nil {
		return nil, ent.err
	}
	return append([]git.Worktree(nil), ent.list...), nil
}

// --- one read of every Compose container ---

// DockerSnapshot is the containers of every Compose project, read at one
// moment with two Docker commands instead of one `docker compose ps` per
// instance.
type DockerSnapshot struct {
	byProject map[string][]compose.Container
}

type allContainersReader interface {
	AllContainers(ctx context.Context) (map[string][]compose.Container, error)
}

// DockerSnapshot reads the containers of all Compose projects. It returns
// (nil, nil) when the Compose adapter cannot do it in one go; callers then
// use Containers per instance, which gives the same answer. An error means
// Docker could not be asked.
func (e *Engine) DockerSnapshot(ctx context.Context) (*DockerSnapshot, error) {
	r, ok := e.compose.(allContainersReader)
	if !ok {
		return nil, nil
	}
	m, err := r.AllContainers(ctx)
	if err != nil {
		return nil, err
	}
	return &DockerSnapshot{byProject: m}, nil
}

// ContainersFrom is Containers for a worktree's instance, taken from snap
// when there is one.
func (e *Engine) ContainersFrom(ctx context.Context, inst *Instance, snap *DockerSnapshot) ([]compose.Container, error) {
	if snap == nil {
		return e.Containers(ctx, inst)
	}
	return append([]compose.Container(nil), snap.byProject[strings.ToLower(inst.Compose)]...), nil
}

// --- cache of `docker compose config` ---

// configCache remembers the resolved configuration of a directory for ttl, as
// long as the files that decide it have not changed. The fingerprint covers
// the Compose files Compose looks for by default, the chosen Compose files
// and the env files; a file they pull in (include, extends, env_file) is only
// noticed when ttl passes.
type configCache struct {
	ttl time.Duration
	now func() time.Time
	mu  sync.Mutex
	m   map[string]configEntry
}

type configEntry struct {
	at  time.Time
	fp  string
	cfg *compose.Project
}

// EnableConfigCache lets the engine remember `docker compose config` answers
// for ttl, but only for reads whose context says a remembered answer is good
// enough (WithCachedConfig): what a panel shows. Anything that acts on the
// configuration (up, down, ports to assign) always asks Compose. It is for
// long-running interfaces that read the same configuration every few seconds
// (the HTTP API). Call it before the engine is shared. ttl <= 0 turns the
// cache off.
func (e *Engine) EnableConfigCache(ttl time.Duration) *Engine {
	if ttl <= 0 {
		e.cfgCache = nil
		return e
	}
	e.cfgCache = &configCache{ttl: ttl, now: time.Now, m: map[string]configEntry{}}
	return e
}

type cachedConfigKey struct{}

// WithCachedConfig returns a context under which resolving an instance may
// use a remembered configuration (see EnableConfigCache).
func WithCachedConfig(ctx context.Context) context.Context {
	return context.WithValue(ctx, cachedConfigKey{}, true)
}

var defaultComposeNames = []string{
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
	"compose.override.yaml", "compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml",
	".env",
}

func configFingerprint(dir string, envFiles, files []string) string {
	paths := make([]string, 0, len(defaultComposeNames)+len(envFiles)+len(files))
	for _, n := range defaultComposeNames {
		paths = append(paths, filepath.Join(dir, n))
	}
	paths = append(paths, envFiles...)
	paths = append(paths, files...)
	var b strings.Builder
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", p, info.ModTime().UnixNano(), info.Size())
		} else {
			b.WriteString(p + ":-;")
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func (c *configCache) key(dir string, envFiles, files []string) string {
	return filepath.Clean(dir) + "\x00" + strings.Join(envFiles, "\x01") + "\x00" + strings.Join(files, "\x01")
}

func (c *configCache) get(key, fp string) *compose.Project {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.m[key]; ok && ent.fp == fp && c.now().Sub(ent.at) < c.ttl {
		return cloneProject(ent.cfg)
	}
	return nil
}

func (c *configCache) put(key, fp string, cfg *compose.Project) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 512 { // bound memory when many directories come and go
		c.m = map[string]configEntry{}
	}
	c.m[key] = configEntry{at: c.now(), fp: fp, cfg: cloneProject(cfg)}
}

// cloneProject copies p deeply, so that a caller that edits its configuration
// cannot change what the cache hands out next.
func cloneProject(p *compose.Project) *compose.Project {
	if p == nil {
		return nil
	}
	out := &compose.Project{Name: p.Name}
	if p.Services != nil {
		out.Services = make(map[string]compose.Service, len(p.Services))
		for name, s := range p.Services {
			c := s
			if s.Build != nil {
				b := *s.Build
				c.Build = &b
			}
			c.Ports = append([]compose.Port(nil), s.Ports...)
			c.Volumes = append([]compose.Volume(nil), s.Volumes...)
			c.Profiles = append([]string(nil), s.Profiles...)
			if s.DependsOn != nil {
				c.DependsOn = make(map[string]json.RawMessage, len(s.DependsOn))
				for k, v := range s.DependsOn {
					c.DependsOn[k] = append(json.RawMessage(nil), v...)
				}
			}
			if s.Networks != nil {
				c.Networks = make(map[string]json.RawMessage, len(s.Networks))
				for k, v := range s.Networks {
					c.Networks[k] = append(json.RawMessage(nil), v...)
				}
			}
			if s.Environment != nil {
				c.Environment = make(map[string]*string, len(s.Environment))
				for k, v := range s.Environment {
					if v != nil {
						cp := *v
						v = &cp
					}
					c.Environment[k] = v
				}
			}
			out.Services[name] = c
		}
	}
	if p.Volumes != nil {
		out.Volumes = make(map[string]compose.VolumeConfig, len(p.Volumes))
		for k, v := range p.Volumes {
			v.External = append(json.RawMessage(nil), v.External...)
			out.Volumes[k] = v
		}
	}
	return out
}
