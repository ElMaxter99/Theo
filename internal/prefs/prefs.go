// Package prefs keeps the person's own preferences for Theo: the tools
// `theo open` starts, whether to look for a new version, and the answer of
// the last look. They live in one small file in Theo's data directory.
package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/filelock"
)

// FileName is the preferences file.
const FileName = "preferences.json"

// Keys that `theo config` accepts, with what each does.
var Keys = map[string]string{
	"open.editor":          "command `theo open --in editor` starts (default: the first of code, cursor, windsurf, zed, subl found)",
	"open.terminal":        "command `theo open --in terminal` starts (default: Windows Terminal, Terminal.app or the system's terminal)",
	"open.files":           "command `theo open --in files` starts (default: explorer, open or xdg-open)",
	"notify.agent":         "off, waiting or all: desktop notification when an agent waits for you (waiting) or also when it finishes (all). Only the desktop app shows it, and only for agents with hooks (default off)",
	"notify.skip-focused":  "on or off: with on, the desktop app does not notify while its window is in front (default off)",
	"ai.provider":          "claude or copilot: the AI command line Theo's tools ask for work (default none: choose one with theo tool ai doctor)",
	"ai.model":             "model of ai.provider (see theo tool ai models <provider>)",
	"ai.roles.coordinator": "provider:model for the coordinator role of the generation, for example claude:opus (default ai.provider and ai.model)",
	"ai.roles.worker":      "provider:model for the worker role of the generation, the cheaper one (default ai.provider and ai.model)",
	"language":             "en, es or system: the language of the reports of the capture tool (default system: the language of the computer, English if it is not Spanish)",
	"update-check":         "on or off: look for a new version of Theo (at start and every few hours; default on in the installed desktop app, off for the command line alone; nothing is sent when off)",
	"update-notify":        "on or off: show a desktop notification when a new version is found (default on; only the desktop app shows it)",
	"update-channel":       "stable or rc: with rc, Theo also offers release candidates (test versions); default stable",
}

// Update is the answer of the last look for a new version.
type Update struct {
	Latest     string    `json:"latest,omitempty"`
	Tag        string    `json:"tag,omitempty"`
	Prerelease bool      `json:"prerelease,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitzero"`
	// Notified is the version a desktop notification was already shown for;
	// Dismissed the one the person closed the banner of.
	Notified  string `json:"notified,omitempty"`
	Dismissed string `json:"dismissed,omitempty"`
}

type data struct {
	Values map[string]string `json:"values,omitempty"`
	Update Update            `json:"update,omitzero"`
}

// Store reads and writes the preferences in Dir.
type Store struct{ Dir string }

func (s Store) path() string { return filepath.Join(s.Dir, FileName) }

func (s Store) read() data {
	var d data
	b, err := os.ReadFile(s.path())
	if err == nil {
		_ = json.Unmarshal(b, &d) // a damaged file only loses preferences
	}
	if d.Values == nil {
		d.Values = map[string]string{}
	}
	return d
}

func (s Store) update(fn func(*data)) error {
	if s.Dir == "" {
		return errors.New("no data directory for preferences")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	unlock, err := filelock.Lock(s.path() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	d := s.read()
	fn(&d)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return filelock.WriteFile(s.path(), append(b, '\n'), 0o600)
}

// Get returns a preference, or "".
func (s Store) Get(key string) string { return s.read().Values[key] }

// All returns every preference that is set.
func (s Store) All() map[string]string { return s.read().Values }

// Set stores a preference. The key must be one of Keys.
func (s Store) Set(key, value string) error {
	if _, ok := Keys[key]; !ok {
		return fmt.Errorf("unknown setting %q (%s)", key, KeyList())
	}
	if key == "update-check" && value != "on" && value != "off" {
		return errors.New("update-check is on or off")
	}
	if key == "update-notify" && value != "on" && value != "off" {
		return errors.New("update-notify is on or off")
	}
	if key == "update-channel" && value != "stable" && value != "rc" {
		return errors.New("update-channel is stable or rc")
	}
	if key == "notify.skip-focused" && value != "on" && value != "off" {
		return errors.New("notify.skip-focused is on or off")
	}
	if key == "language" && value != "en" && value != "es" && value != "system" {
		return errors.New("language is en, es or system")
	}
	if key == "notify.agent" && value != "off" && value != "waiting" && value != "all" {
		return errors.New("notify.agent is off, waiting or all")
	}
	if key == "ai.provider" && value != "claude" && value != "copilot" {
		return errors.New("ai.provider is claude or copilot")
	}
	if key == "ai.roles.coordinator" || key == "ai.roles.worker" {
		p, m, _ := strings.Cut(value, ":")
		if p != "claude" && p != "copilot" {
			return fmt.Errorf("%s is provider:model with provider claude or copilot, for example claude:opus", key)
		}
		_ = m
	}
	if value == "" {
		return errors.New("an empty value: use `theo config unset` to go back to the default")
	}
	return s.update(func(d *data) { d.Values[key] = value })
}

// Unset returns a preference to its default.
func (s Store) Unset(key string) error {
	if _, ok := Keys[key]; !ok {
		return fmt.Errorf("unknown setting %q (%s)", key, KeyList())
	}
	return s.update(func(d *data) { delete(d.Values, key) })
}

// UpdateInfo returns the last answer of the version check.
func (s Store) UpdateInfo() Update { return s.read().Update }

// SetUpdateInfo records an answer of the version check.
func (s Store) SetUpdateInfo(u Update) error { return s.update(func(d *data) { d.Update = u }) }

// ChangeUpdateInfo changes the answer of the version check under the lock, so
// that a notice and a check running together do not lose each other's fields.
func (s Store) ChangeUpdateInfo(fn func(*Update)) error {
	return s.update(func(d *data) { fn(&d.Update) })
}

// KeyList names the settings.
func KeyList() string {
	var names []string
	for k := range Keys {
		names = append(names, k)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
