package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkByID(checks []Check, id string) (Check, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

func TestV5ChecksGitSpeed(t *testing.T) {
	old := v5Checks(DoctorEnv{}, "git version 2.39.2")
	if c, ok := checkByID(old, "git-speed"); !ok || c.Status != StatusWarn {
		t.Errorf("old git: %+v", old)
	}
	if _, ok := checkByID(v5Checks(DoctorEnv{}, "git version 2.45.0"), "git-speed"); ok {
		t.Error("a recent Git must not warn")
	}
}

func TestV5ChecksRepoConfigAndHooks(t *testing.T) {
	good, bad, home := t.TempDir(), t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(good, ".theo.json"), []byte(`{"copy":[".env"]}`), 0o644)
	os.WriteFile(filepath.Join(bad, ".theo.json"), []byte(`{`), 0o644)
	env := DoctorEnv{Projects: []string{good, bad}, Home: home}

	checks := v5Checks(env, "git version 2.45.0")
	if c, ok := checkByID(checks, "repo-config"); !ok || c.Status != StatusWarn || !strings.Contains(c.Detail, filepath.Base(bad)) {
		t.Errorf("repo-config: %+v", c)
	}
	if c, ok := checkByID(checks, "claude-hooks"); !ok || c.Status != StatusInfo || c.Fix == "" {
		t.Errorf("hooks missing: %+v", c)
	}

	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"theo hook agent-state"}]}]}}`), 0o644)
	if c, _ := checkByID(v5Checks(env, "git version 2.45.0"), "claude-hooks"); c.Status != StatusOK {
		t.Errorf("hooks installed: %+v", c)
	}
}
