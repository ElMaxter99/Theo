// Package instances stores what Theo keeps about each instance outside the
// project's files: its settings (environment variables, container names) and
// the history of operations.
package instances

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

// Empty reports whether nothing is configured.
func (c Config) Empty() bool { return len(c.Env) == 0 && !c.NeutralizeContainerNames }

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
)

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
