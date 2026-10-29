// Package instances stores what Theo keeps about each instance outside the
// project's files: its settings (environment variables, container names) and
// the history of operations.
package instances

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Var is an environment variable of an instance.
type Var struct {
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"` // masked when shown
	// Services limits the variable to these services. Empty means every
	// service of the project.
	Services []string `json:"services,omitempty"`
}

// Config is what Theo keeps about one instance.
type Config struct {
	// Project is the Theo project the instance belongs to, so its settings can
	// be told apart after its worktree is gone.
	Project string         `json:"project,omitempty"`
	Env     map[string]Var `json:"env,omitempty"`
	// NeutralizeContainerNames removes fixed container_name settings, which
	// would make two instances collide.
	NeutralizeContainerNames bool `json:"neutralize_container_names,omitempty"`
	// Shared lists the services this instance does not run because it uses
	// the project's shared ones. Join lists the services of this instance
	// that join the shared network, besides the ones that depended on a
	// shared service.
	Shared []string `json:"shared,omitempty"`
	Join   []string `json:"join,omitempty"`
	// AutoPorts keeps host ports reassigned when they are taken. It is set the
	// first time the instance starts with --auto-ports, so that later starts
	// keep the address instead of going back to the configured port.
	AutoPorts bool `json:"auto_ports,omitempty"`
	// Profiles are the Compose profiles the instance always starts with, on
	// top of the ones given to `up`.
	Profiles []string `json:"profiles,omitempty"`
}

// Empty reports whether nothing is configured.
func (c Config) Empty() bool {
	return len(c.Env) == 0 && !c.NeutralizeContainerNames && len(c.Shared) == 0 && len(c.Join) == 0 && !c.AutoPorts && len(c.Profiles) == 0
}

// SharedSet is the group of services a project shares between instances.
// One dedicated Compose project runs them, from the Compose files of the
// From worktree, and the instances that use them are linked in their
// Config.
type SharedSet struct {
	From     string   `json:"from"`              // worktree path whose Compose files define the services
	Services []string `json:"services"`          // names of the shared services
	Network  string   `json:"network"`           // Docker network that connects the shared services and their consumers
	Publish  bool     `json:"publish,omitempty"` // keep the host ports the project publishes
}

// SharedState maps a project (lower case name) to its shared set.
type SharedState map[string]SharedSet

// State maps an instance (the path of its worktree) to its configuration.
type State map[string]Config

// Event is an entry of the operation history.
type Event struct {
	Time      time.Time `json:"time"`
	User      string    `json:"user,omitempty"`
	Project   string    `json:"project"`
	Workspace string    `json:"workspace"`
	Compose   string    `json:"compose_project,omitempty"`
	Operation string    `json:"operation"`
	Result    string    `json:"result"` // "ok" or "error"
	Error     string    `json:"error,omitempty"`
}

// Store reads and writes the instance settings and the history in a
// directory.
type Store struct {
	dir string
}

const (
	configFile  = "instances.json"
	historyFile = "history.jsonl"
	sharedFile  = "shared.json"
)

type sharedDocument struct {
	Version int         `json:"version"`
	Shared  SharedState `json:"shared"`
}

// LoadShared returns the shared sets; there are none before the first save.
func (s *Store) LoadShared() (SharedState, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, sharedFile))
	if errors.Is(err, os.ErrNotExist) {
		return SharedState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc sharedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", filepath.Join(s.dir, sharedFile), err)
	}
	if doc.Shared == nil {
		doc.Shared = SharedState{}
	}
	return doc.Shared, nil
}

// SaveShared replaces the shared sets.
func (s *Store) SaveShared(st SharedState) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sharedDocument{Version: 1, Shared: st}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, sharedFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Open returns a store backed by files in dir, created on first write.
func Open(dir string) *Store { return &Store{dir: dir} }

type document struct {
	Version   int   `json:"version"`
	Instances State `json:"instances"`
}

// Load returns the saved settings; there are none before the first save.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, configFile))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", filepath.Join(s.dir, configFile), err)
	}
	if doc.Instances == nil {
		doc.Instances = State{}
	}
	return doc.Instances, nil
}

// Save replaces the settings. The file may hold secrets, so only its owner
// can read it.
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document{Version: 1, Instances: st}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, configFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Append adds an event to the history.
func (s *Store) Append(ev Event) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, historyFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Events returns the whole history, oldest first. Lines that cannot be read
// are skipped.
func (s *Store) Events() ([]Event, error) {
	f, err := os.Open(filepath.Join(s.dir, historyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var list []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			list = append(list, ev)
		}
	}
	return list, sc.Err()
}

// RenameProject changes the project name in the settings and in every event
// of the history, for `theo project rename`. It returns how many records it
// changed. The names are compared ignoring case.
func (s *Store) RenameProject(oldName, newName string) (int, error) {
	n := 0
	state, err := s.Load()
	if err != nil {
		return 0, err
	}
	for ws, cfg := range state {
		if strings.EqualFold(cfg.Project, oldName) {
			cfg.Project = newName
			state[ws] = cfg
			n++
		}
	}
	if n > 0 {
		if err := s.Save(state); err != nil {
			return 0, err
		}
	}
	changed, err := s.renameEvents(oldName, newName)
	if err != nil {
		return 0, err
	}
	shared, err := s.LoadShared()
	if err != nil {
		return 0, err
	}
	if set, ok := shared[strings.ToLower(oldName)]; ok {
		delete(shared, strings.ToLower(oldName))
		shared[strings.ToLower(newName)] = set
		if err := s.SaveShared(shared); err != nil {
			return 0, err
		}
		n++
	}
	return n + changed, nil
}

// renameEvents rewrites the history file with the new project name. Lines
// that cannot be read are kept as they are. The file is replaced only after
// the new one is completely written.
func (s *Store) renameEvents(oldName, newName string) (int, error) {
	path := filepath.Join(s.dir, historyFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var out bytes.Buffer
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev Event
		if json.Unmarshal(line, &ev) == nil && strings.EqualFold(ev.Project, oldName) {
			ev.Project = newName
			if renamed, err := json.Marshal(ev); err == nil {
				line = renamed
				n++
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	err = sc.Err()
	f.Close()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return n, os.Rename(tmp, path)
}
