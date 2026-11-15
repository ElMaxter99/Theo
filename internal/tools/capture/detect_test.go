package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		kind       string
		start      string
		install    string
		confidence string
	}{
		{"compose", map[string]string{"docker-compose.yml": "services: {}"}, "compose", "", "", "high"},
		{"vite with npm lock", map[string]string{
			"package.json":      `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`,
			"package-lock.json": "{}"}, "script", "npm run dev -- --port ${PORT} --strictPort", "npm ci", "medium"},
		{"next with pnpm", map[string]string{
			"package.json":   `{"scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`,
			"pnpm-lock.yaml": ""}, "script", "pnpm run dev -- -p ${PORT}", "pnpm install --frozen-lockfile", "medium"},
		{"yarn unknown framework", map[string]string{
			"package.json": `{"scripts":{"start":"node server.js"}}`,
			"yarn.lock":    ""}, "script", "yarn start", "yarn install --frozen-lockfile", "low"},
		{"makefile", map[string]string{"Makefile": "build:\n\tx\n\ndev:\n\ty\n"}, "script", "make dev", "", "low"},
	}
	for _, c := range cases {
		d, err := Detect(project(t, c.files))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d.Run == nil || d.Run.Kind != c.kind || d.Run.Start != c.start || d.Confidence != c.confidence {
			t.Errorf("%s: got %+v run %+v", c.name, d, d.Run)
			continue
		}
		if c.install != "" && (len(d.Run.Install) != 1 || d.Run.Install[0] != c.install) {
			t.Errorf("%s: install %v, want %s", c.name, d.Run.Install, c.install)
		}
		if err := d.Run.Validate(); err != nil {
			t.Errorf("%s: the proposal must be valid: %v", c.name, err)
		}
	}
}

func TestDetectNothingAndPrecedence(t *testing.T) {
	d, err := Detect(project(t, map[string]string{"README.md": "x"}))
	if err != nil || d.Run != nil || d.Confidence != "none" || len(d.Notes) == 0 {
		t.Errorf("nothing to detect: %+v %v", d, err)
	}
	// Compose wins over package.json, and the note says so.
	d, err = Detect(project(t, map[string]string{
		"compose.yaml": "services: {}",
		"package.json": `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`}))
	if err != nil || d.Run.Kind != "compose" || !strings.Contains(strings.Join(d.Notes, " "), "Compose was preferred") {
		t.Errorf("compose precedence: %+v %v", d, err)
	}
	// What the repository declares is kept.
	d, err = Detect(project(t, map[string]string{".theo.json": `{"run":{"start":"go run ."}}`, "package.json": `{"scripts":{"dev":"vite"}}`}))
	if err != nil || d.Run.Start != "go run ." || !strings.Contains(d.Evidence[0], "already") {
		t.Errorf("declared section: %+v %v", d, err)
	}
	// A broken package.json is reported, not fatal.
	d, err = Detect(project(t, map[string]string{"package.json": "{"}))
	if err != nil || d.Confidence != "none" || !strings.Contains(strings.Join(d.Notes, " "), "not valid JSON") {
		t.Errorf("broken package.json: %+v %v", d, err)
	}
}

func TestDetectAngularJSStatic(t *testing.T) {
	dir := project(t, map[string]string{
		"package.json":      `{"scripts":{"build":"gulp build"},"dependencies":{"angular":"^1.8.3"}}`,
		"package-lock.json": "{}",
	})
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.html"), []byte(`<html><head><base href="/portal/"></head></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := d.Run
	if r == nil || r.Kind != "static" || r.Dir != "dist" || r.Base != "/portal" || r.Install[0] != "npm ci" || r.Build[0] != "npm run build" || r.Ready == nil || r.Ready.Path != "/portal/" {
		t.Fatalf("%+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Evidence) != 2 || !strings.Contains(d.Evidence[0], "angular 1.8.3 (AngularJS)") || !strings.Contains(d.Evidence[1], `src/index.html: <base href="/portal/">`) {
		t.Fatalf("%v", d.Evidence)
	}
}
