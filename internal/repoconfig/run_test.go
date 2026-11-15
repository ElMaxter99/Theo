package repoconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadRunSection(t *testing.T) {
	dir := writeConfig(t, `{"version":1,"run":{"install":["npm ci"],"start":" npm run dev -- --port ${PORT} ","ready":{"path":"/health"}}}`)
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Run == nil || l.Empty() {
		t.Fatalf("run must be read and make the file not empty: %+v", l)
	}
	r := l.Run
	if r.Kind != RunScript || r.Start != "npm run dev -- --port ${PORT}" || len(r.Install) != 1 {
		t.Errorf("normalised section: %+v", r)
	}
	if r.PortEnv() != "PORT" || !r.AutoPort() || r.ReadyPath() != "/health" || r.ReadyTimeout() != DefaultReadyTimeout {
		t.Errorf("defaults: env %s auto %v path %s timeout %d", r.PortEnv(), r.AutoPort(), r.ReadyPath(), r.ReadyTimeout())
	}
	if got := r.ReadyStatus(); len(got) != 2 || got[0] != 200 || got[1] != 304 {
		t.Errorf("default status: %v", got)
	}
}

func TestLoadRunErrors(t *testing.T) {
	cases := map[string]string{
		`{"run":{"kind":"vm"}}`:                             "not known",
		`{"run":{"kind":"script"}}`:                         `needs "start"`,
		`{"run":{"kind":"compose","start":"x"}}`:            "belongs to kind",
		`{"run":{"start":"x","port":{"auto":false}}}`:       "port.fixed",
		`{"run":{"start":"x","ready":{"path":"health"}}}`:   "must start with /",
		`{"run":{"start":"x","ready":{"status":[42]}}}`:     "HTTP status",
		`{"run":{"start":"x","ready":{"timeout_s":99999}}}`: "out of range",
		`{"run":{"start":"x","install":[" "]}}`:             "empty command",
		`{"run":{"start":"x","env":{"A B":"1"}}}`:           "not valid",
	}
	for body, want := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error containing %q, got %v", body, want, err)
		}
	}
}

func TestRunComposeNeedsNoStart(t *testing.T) {
	l, err := Load(writeConfig(t, `{"run":{"service":"web","ready":{"timeout_s":30}}}`))
	if err != nil || l.Run == nil || l.Run.Kind != RunCompose || l.Run.Service != "web" {
		t.Fatalf("compose section: %+v %v", l.Run, err)
	}
}

func TestRunFingerprintChangesWithWhatRuns(t *testing.T) {
	a := RunSpec{Start: "npm start", Env: map[string]string{"A": "1"}}
	same := RunSpec{Start: "npm start", Env: map[string]string{"A": "1"}}
	if RunFingerprint(a) != RunFingerprint(same) {
		t.Error("the same section must give the same fingerprint")
	}
	for name, b := range map[string]RunSpec{
		"start":   {Start: "npm run dev", Env: a.Env},
		"env":     {Start: "npm start", Env: map[string]string{"A": "2"}},
		"install": {Start: "npm start", Install: []string{"npm ci"}, Env: a.Env},
	} {
		if RunFingerprint(a) == RunFingerprint(b) {
			t.Errorf("a change of %s must ask for approval again", name)
		}
	}
}

func TestLoadStaticSection(t *testing.T) {
	l, err := Load(writeConfig(t, `{"run":{"kind":"static","install":["npm ci"],"build":["npm run build"],"dir":"dist","base":"/portal/","spa":true,"proxy":{"/portal/api":"http://localhost:4000"}}}`))
	if err != nil || l.Run == nil {
		t.Fatalf("%+v %v", l.Run, err)
	}
	r := l.Run
	if r.Kind != RunStatic || r.Base != "/portal" || r.Dir != "dist" || !r.SPA || r.ReadyPath() != "/portal/" {
		t.Errorf("normalised static section: %+v ready %s", r, r.ReadyPath())
	}
	if got := r.Commands(); len(got) != 2 || got[1] != "npm run build" {
		t.Errorf("the build is a command that needs approval: %v", got)
	}
	// The kind may be left out when the section has a dir and no start.
	if l, err := Load(writeConfig(t, `{"run":{"dir":"dist","base":"/"}}`)); err != nil || l.Run.Kind != RunStatic || l.Run.ReadyPath() != "/" {
		t.Errorf("implicit static: %+v %v", l.Run, err)
	}
}

func TestLoadStaticErrors(t *testing.T) {
	cases := map[string]string{
		`{"run":{"kind":"static","base":"/portal"}}`:                                    `needs "build" or "dir"`,
		`{"run":{"kind":"static","dir":"dist"}}`:                                        `"base" starting with /`,
		`{"run":{"kind":"static","build":["x"],"base":"/p"}}`:                           `needs "dir"`,
		`{"run":{"kind":"static","dir":"dist","base":"/p","start":"x"}}`:                `has no "start"`,
		`{"run":{"kind":"static","dir":"../x","base":"/p"}}`:                            "inside the worktree",
		`{"run":{"kind":"static","dir":"dist","base":"/p","build":[" "]}}`:              "empty command",
		`{"run":{"kind":"static","dir":"dist","base":"/p","proxy":{"api":"http://a"}}}`: "must start with /",
		`{"run":{"kind":"static","dir":"dist","base":"/p","proxy":{"/api":"ftp://a"}}}`: "http:// or https://",
		`{"run":{"kind":"script","start":"x","dir":"dist"}}`:                            `belong to kind "static"`,
	}
	for body, want := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error containing %q, got %v", body, want, err)
		}
	}
}
