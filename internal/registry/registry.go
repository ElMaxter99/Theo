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
	// SetupTrusted identifies the after_create commands of .theo.json that
	// the person approved for this project. Commands run code from the
	// repository, so a changed list asks again.
	SetupTrusted string `json:"setup_trusted,omitempty"`
	// Workspaces are the workspaces Theo records beyond what Git lists: the
	// independent clones it created, and the owner labels of any workspace.
	Workspaces []Workspace `json:"workspaces,omitempty"`
	// ComposeFiles are the Compose files of the project, relative to the
	// root of each worktree and in the order Compose merges them (like
	// repeated -f flags). Empty means: let Compose find them.
	ComposeFiles []string `json:"compose_files,omitempty"`
	// URLInData says whether the project's application stores its own
	// address in its data (Q-020): "" or "auto" (decided by the image),
	// "on" or "off".
	URLInData string `json:"url_in_data,omitempty"`
	// InactiveAfterDays is how many days without activity make a workspace
	// inactive (1 to 90). 0 means the default, 7.
	InactiveAfterDays int `json:"inactive_after_days,omitempty"`
	// Parked are the worktrees put away with `theo worktree park`: their
	// directory is gone, their branch stays, and `worktree resume` brings
	// them back.
	Parked  []Parked  `json:"parked,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// Parked is a worktree put away with `theo worktree park`.
type Parked struct {
	Branch   string    `json:"branch"`
	Path     string    `json:"path"` // where it was, and where resume puts it back
	Base     string    `json:"base,omitempty"`
	Owner    string    `json:"owner,omitempty"`
	Note     string    `json:"note,omitempty"`
	ParkedAt time.Time `json:"parked_at"`
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
// record holds what Git does not: the owner label, who created it, whether
// it is kept and when its owner last asked Theo about it.
type Workspace struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	Owner     string    `json:"owner,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	// Origin says who created it: "theo" for `theo worktree add` (or MCP
	// create_worktree); empty when Theo found it.
	Origin string `json:"origin,omitempty"`
	// Kept is set by `theo worktree keep`: it is never offered for cleaning.
	// KeptUntil, when set, is when the mark expires.
	Kept      bool      `json:"kept,omitempty"`
	KeptUntil time.Time `json:"kept_until,omitzero"`
	KeepNote  string    `json:"keep_note,omitempty"`
	// LastSeen is the last time its owner called Theo's MCP server about it.
	LastSeen time.Time `json:"last_seen,omitzero"`
}

// Says reports whether the record holds anything beyond its path and kind,
// so it is worth keeping for a workspace Git lists itself.
func (w Workspace) Says() bool {
	return w.Kind == KindClone || w.Owner != "" || w.Origin != "" || w.Kept || w.KeepNote != "" || !w.LastSeen.IsZero()
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
