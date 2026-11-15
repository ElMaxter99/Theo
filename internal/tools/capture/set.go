package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SetsDir and ReportsDir are where a worktree keeps captures and reports.
const (
	SetsDir    = ".theo/capturas/conjuntos"
	ReportsDir = ".theo/capturas/informes"
)

// Shot status values.
const (
	ShotOK       = "ok"
	ShotUnstable = "unstable"
	ShotFailed   = "failed"
)

// Shot is one screenshot: a state at a viewport.
type Shot struct {
	Screen   string `json:"screen"`
	State    string `json:"state"`
	Viewport string `json:"viewport"`
	File     string `json:"file,omitempty"` // relative to the set folder
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"` // why it failed or is unstable
	Masks    []Rect `json:"masks,omitempty"`  // ignored areas, in pixels
}

// ScreenInfo is what a set remembers of each screen of its inventory.
type ScreenInfo struct {
	ID     string   `json:"id"`
	Title  string   `json:"title,omitempty"`
	Route  string   `json:"route"`
	Origin []string `json:"origin,omitempty"`
	States []string `json:"states"`
}

// Manifest describes a set of captures: one deployment, one pass.
type Manifest struct {
	Version   int          `json:"version"`
	Label     string       `json:"label"`
	App       string       `json:"app"`
	Commit    string       `json:"commit,omitempty"`
	Date      string       `json:"date"`
	Mode      string       `json:"mode"`     // mock, real or grabar
	Chromium  string       `json:"chromium"` // browser version
	Entry     string       `json:"entry,omitempty"`
	Viewports []Viewport   `json:"viewports"`
	Screens   []ScreenInfo `json:"screens"`
	Flows     []Flow       `json:"flows,omitempty"`
	Shots     []Shot       `json:"shots"`
}

// Set is a manifest and the folder it lives in.
type Set struct {
	Dir string
	Manifest
}

// ShotFile is the file name of a screenshot inside a set.
func ShotFile(screen, state, viewport string) string {
	return "shots/" + safeName(screen) + "--" + safeName(state) + "--" + safeName(viewport) + ".png"
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

// Counts returns how many shots are captured, unstable and failed.
func (m *Manifest) Counts() (ok, unstable, failed int) {
	for _, s := range m.Shots {
		switch s.Status {
		case ShotOK:
			ok++
		case ShotUnstable:
			unstable++
		default:
			failed++
		}
	}
	return
}

// Shot finds a shot.
func (m *Manifest) Shot(screen, state, viewport string) *Shot {
	for i := range m.Shots {
		s := &m.Shots[i]
		if s.Screen == screen && s.State == state && s.Viewport == viewport {
			return s
		}
	}
	return nil
}

// Screen finds a screen by id.
func (m *Manifest) Screen(id string) *ScreenInfo {
	for i := range m.Screens {
		if m.Screens[i].ID == id {
			return &m.Screens[i]
		}
	}
	return nil
}

// Save writes the manifest into the set's folder.
func (s *Set) Save() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "manifest.json"), append(data, '\n'), 0o644)
}

// ErrNoSet means no set has that label.
var ErrNoSet = errors.New("no set with that label")

// LoadSet reads the set folder dir.
func LoadSet(dir string) (*Set, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	s := &Set{Dir: dir}
	if err := json.Unmarshal(data, &s.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "manifest.json"), err)
	}
	return s, nil
}

// ListSets returns the sets of a worktree, newest first.
func ListSets(root string) ([]*Set, error) {
	base := filepath.Join(root, filepath.FromSlash(SetsDir))
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Set
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := LoadSet(filepath.Join(base, e.Name())); err == nil {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}

// FindSet returns the newest set with that label.
func FindSet(root, label string) (*Set, error) {
	sets, err := ListSets(root)
	if err != nil {
		return nil, err
	}
	for _, s := range sets {
		if s.Label == label {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%w: %q (see `theo tool capture sets`)", ErrNoSet, label)
}

// Image opens a screenshot of the set.
func (s *Set) Image(file string) (image.Image, error) {
	f, err := os.Open(filepath.Join(s.Dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// SetFolder is the folder name of a set: label and short commit.
func SetFolder(label, commit string) string {
	name := safeName(label)
	if commit != "" {
		name += "-" + commit
	}
	return name
}
