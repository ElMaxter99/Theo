// Package registry stores the projects registered in Theo in a JSON file in
// the user's configuration directory.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EnvHome overrides the directory where Theo keeps its data.
const EnvHome = "THEO_HOME"

const fileName = "registry.json"

// Project is a registered project.
type Project struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	EnvFile string `json:"env_file,omitempty"` // shared by worktrees without their own .env
	// WorktreeDir is where `theo worktree add` creates worktrees for the
	// project. Empty means Theo's own directory (see engine.WorktreeDir).
	WorktreeDir string `json:"worktree_dir,omitempty"`
	// CopyOnAdd lists root-relative file patterns that `theo worktree add
	// --copy` copies from the main worktree into a new workspace. Empty
	// means nothing is copied.
	CopyOnAdd []string `json:"copy_on_add,omitempty"`
	// Workspaces are the workspaces Theo records beyond what Git lists: the
	// independent clones it created, and the owner labels of any workspace.
	Workspaces []Workspace `json:"workspaces,omitempty"`
	// ComposeFiles are the Compose files of the project, relative to the
	// root of each worktree and in the order Compose merges them (like
	// repeated -f flags). Empty means: let Compose find them.
	ComposeFiles []string  `json:"compose_files,omitempty"`
	AddedAt      time.Time `json:"added_at"`
}

// Workspace kinds kept in the registry.
const (
	KindClone = "clone"
	// KindWorktree and KindMain only appear in records that carry an owner
	// label for a workspace that Git lists itself.
	KindWorktree = "worktree"
	KindMain     = "main"
)

// Workspace is what Theo records about one workspace of a project. A clone
// exists only through this record (Git does not know it); for the others the
// record only holds the owner label.
type Workspace struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	Owner     string    `json:"owner,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
}

type document struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// Store reads and writes the registry file.
type Store struct {
	path string
}

// DefaultDir returns the directory where Theo keeps its data: $THEO_HOME if
// set, otherwise "theo" inside the user's configuration directory.
func DefaultDir() (string, error) {
	if dir := os.Getenv(EnvHome); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the user configuration directory (set %s): %w", EnvHome, err)
	}
	return filepath.Join(base, "theo"), nil
}

// Open returns a store backed by the registry file in dir. The file is
// created on the first save.
func Open(dir string) *Store {
	return &Store{path: filepath.Join(dir, fileName)}
}

// Path returns the location of the registry file.
func (s *Store) Path() string {
	return s.path
}

// Load returns the registered projects. A missing file is an empty registry.
func (s *Store) Load() ([]Project, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the registry: %w", err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("registry %s is corrupt: %w", s.path, err)
	}
	return doc.Projects, nil
}

// Save replaces the registered projects. The file is written to a temporary
// file first and then renamed, so a failed write never leaves it half done.
func (s *Store) Save(projects []Project) error {
	if projects == nil {
		projects = []Project{}
	}
	data, err := json.MarshalIndent(document{Version: 1, Projects: projects}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(s.path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write the registry: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write the registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write the registry: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("cannot write the registry: %w", err)
	}
	return nil
}
