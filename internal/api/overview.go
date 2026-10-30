package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// worktreeID is the id of a worktree in URLs: wt_ and the first 10 hex
// digits of the SHA-256 of its canonical absolute path.
func worktreeID(path string) string {
	p := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	sum := sha256.Sum256([]byte(p))
	return "wt_" + hex.EncodeToString(sum[:])[:10]
}

func instanceKey(project, wtID string) string { return strings.ToLower(project) + "/" + wtID }

// resolveWorktree finds a worktree of a project by id. Ids are computed from
// Worktrees() on each request; nothing is remembered.
func (s *Server) resolveWorktree(ctx context.Context, project, id string) (engine.Worktree, error) {
	if _, err := s.eng.Project(project); err != nil {
		return engine.Worktree{}, projectNotFound(project)
	}
	list, err := s.eng.Worktrees(ctx, project)
	if err != nil {
		return engine.Worktree{}, err
	}
	for _, wt := range list {
		if worktreeID(wt.Path) == id {
			return wt, nil
		}
	}
	return engine.Worktree{}, worktreeNotFound(project, id)
}

// DockerInfo says whether Docker could be asked.
type DockerInfo struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// BusyRef is the operation running on an instance.
type BusyRef struct {
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
}

// ContainerCount counts the containers of an instance.
type ContainerCount struct {
	Running int `json:"running"`
	Total   int `json:"total"`
}

// WorktreeView is a worktree and its instance, as the panel shows it.
type WorktreeView struct {
	ID             string                 `json:"id"`
	Path           string                 `json:"path"`
	Branch         string                 `json:"branch,omitempty"`
	Head           string                 `json:"head,omitempty"`
	Detached       bool                   `json:"detached"`
	Locked         bool                   `json:"locked"`
	Prunable       bool                   `json:"prunable"`
	Bare           bool                   `json:"bare"`
	Main           bool                   `json:"main"`
	Kind           string                 `json:"kind"`
	Owner          string                 `json:"owner,omitempty"`
	Dirty          bool                   `json:"dirty"`
	ComposeProject string                 `json:"compose_project,omitempty"`
	EnvFile        *engine.EnvFile        `json:"env_file,omitempty"`
	State          string                 `json:"state"`
	Containers     ContainerCount         `json:"containers"`
	Ports          []engine.PortStatus    `json:"ports"`
	Warnings       []engine.StatusWarning `json:"warnings"`
	Error          string                 `json:"error,omitempty"`
	Busy           *BusyRef               `json:"busy"`
}

// Overview is the answer of GET /projects/{p}/overview.
type Overview struct {
	Project     string         `json:"project"`
	Path        string         `json:"path"`
	Docker      DockerInfo     `json:"docker"`
	Worktrees   []WorktreeView `json:"worktrees"`
	GeneratedAt time.Time      `json:"generated_at"`
}

// signature identifies the content of an overview without its time, for the
// poller.
func (o *Overview) signature() string {
	c := *o
	c.GeneratedAt = time.Time{}
	for i := range c.Worktrees {
		c.Worktrees[i].Busy = nil
	}
	b, _ := json.Marshal(c)
	return string(b)
}

type overviewCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]cached
}

type cached struct {
	at time.Time
	ov *Overview
}

func newOverviewCache(ttl time.Duration) *overviewCache {
	return &overviewCache{ttl: ttl, m: map[string]cached{}}
}

func (c *overviewCache) get(p string) *Overview {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[strings.ToLower(p)]; ok && time.Since(e.at) < c.ttl {
		return e.ov
	}
	return nil
}

func (c *overviewCache) put(p string, ov *Overview) {
	c.mu.Lock()
	c.m[strings.ToLower(p)] = cached{time.Now(), ov}
	c.mu.Unlock()
}

func (c *overviewCache) invalidateAll() {
	c.mu.Lock()
	c.m = map[string]cached{}
	c.mu.Unlock()
}

func (c *overviewCache) invalidate(p string) {
	c.mu.Lock()
	delete(c.m, strings.ToLower(p))
	c.mu.Unlock()
}

// overview builds the panel of a project with one pass over its worktrees:
// the worktrees from Git, and for each one the containers (docker compose ps)
// and the port plan. Worktrees are looked at in parallel. It never runs
// `docker compose ls` per worktree. A failing Docker makes the states
// unknown; the registry data is still returned.
func (s *Server) overview(ctx context.Context, project string, fresh bool) (*Overview, error) {
	p, err := s.eng.Project(project)
	if err != nil {
		return nil, projectNotFound(project)
	}
	if !fresh {
		if ov := s.cache.get(p.Name); ov != nil {
			return s.withBusy(ov), nil
		}
	}
	list, err := s.eng.Worktrees(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	ov := &Overview{Project: p.Name, Path: p.Path, Docker: DockerInfo{Available: true}, Worktrees: make([]WorktreeView, len(list)), GeneratedAt: time.Now().UTC()}
	var dockerMu sync.Mutex
	dockerDown := func(err error) {
		dockerMu.Lock()
		if ov.Docker.Available {
			ov.Docker = DockerInfo{Available: false, Error: firstLine(err.Error())}
		}
		dockerMu.Unlock()
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, wt := range list {
		v := WorktreeView{ID: worktreeID(wt.Path), Path: wt.Path, Branch: wt.Branch, Head: wt.Head, Detached: wt.Detached, Locked: wt.Locked,
			Prunable: wt.Prunable, Bare: wt.Bare, Main: wt.Kind == engine.KindMain, Kind: wt.Kind, Owner: wt.Owner, Dirty: wt.Dirty,
			State: engine.InstUnknown, Ports: []engine.PortStatus{}, Warnings: []engine.StatusWarning{}}
		ov.Worktrees[i] = v
		if wt.Bare || wt.Prunable {
			ov.Worktrees[i].State = engine.InstAbsent
			if wt.Prunable {
				ov.Worktrees[i].Warnings = append(ov.Worktrees[i].Warnings, engine.StatusWarning{Kind: "worktree missing", Message: "Git lists this worktree but its directory is gone"})
			}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, wt engine.Worktree) {
			defer wg.Done()
			defer func() { <-sem }()
			s.fillInstance(ctx, p.Name, wt, &ov.Worktrees[i], dockerDown)
		}(i, wt)
	}
	wg.Wait()
	s.cache.put(p.Name, ov)
	return s.withBusy(ov), nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// withBusy returns a copy of the overview with the current operations.
func (s *Server) withBusy(ov *Overview) *Overview {
	c := *ov
	c.Worktrees = append([]WorktreeView(nil), ov.Worktrees...)
	for i := range c.Worktrees {
		c.Worktrees[i].Busy = s.busyRef(ov.Project, c.Worktrees[i].ID)
	}
	return &c
}

func (s *Server) busyRef(project, id string) *BusyRef {
	if op := s.ops.busy(instanceKey(project, id)); op != nil {
		return &BusyRef{OperationID: op.ID, Kind: op.Kind}
	}
	return nil
}

func (s *Server) fillInstance(ctx context.Context, project string, wt engine.Worktree, v *WorktreeView, dockerDown func(error)) {
	warn := func(kind, msg string) {
		v.Warnings = append(v.Warnings, engine.StatusWarning{Kind: kind, Message: msg})
	}
	inst, err := s.eng.InstanceFor(ctx, project, wt.Path, engine.InstanceOptions{})
	if err != nil {
		v.Error = firstLine(err.Error())
		warn("config-error", firstLine(err.Error()))
		return
	}
	v.ComposeProject = inst.Compose
	env := inst.EnvFile
	v.EnvFile = &env
	containers, err := s.eng.Containers(ctx, inst)
	if err != nil {
		dockerDown(err)
		return
	}
	v.State = engine.InstanceState(containers, s.eng.ExpectedServices(inst), true)
	v.Containers.Total = len(containers)
	for _, c := range containers {
		if c.State == "running" {
			v.Containers.Running++
		}
	}
	plan, err := s.eng.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return
	}
	for _, m := range plan.Mappings {
		port := m.Effective
		if m.Running != 0 {
			port = m.Running
		}
		v.Ports = append(v.Ports, engine.PortStatus{Service: m.Service, ContainerPort: m.Target, Protocol: m.Protocol, HostPort: port, URL: m.URL(), Status: m.Status})
	}
	for _, m := range plan.Conflicts() {
		warn("port-conflict", "host port "+m.Configured+" ("+m.Service+") is already in use")
	}
}

// metaCache remembers the answer about Docker and Git for a few seconds.
type metaCache struct {
	mu   sync.Mutex
	at   time.Time
	data Meta
}

// Meta is the answer of GET /meta.
type Meta struct {
	TheoVersion string   `json:"theo_version"`
	APIVersion  string   `json:"api_version"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	Docker      ToolInfo `json:"docker"`
	Compose     ToolInfo `json:"compose"`
	Git         ToolInfo `json:"git"`
	DataDir     string   `json:"data_dir"`
	Idle        IdleInfo `json:"idle"`
}

// ToolInfo is whether a tool works and its version.
type ToolInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	// MinVersion is the oldest version Theo works with (Compose and Git).
	MinVersion string `json:"min_version,omitempty"`
	Error      string `json:"error,omitempty"`
}

// IdleInfo says when the server stops by itself.
type IdleInfo struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}
