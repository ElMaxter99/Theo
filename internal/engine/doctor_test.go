package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// fakeTools answers the tools the doctor runs, by "name arg arg" prefix.
type fakeTools map[string]string

func (f fakeTools) run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	for prefix, out := range f {
		if strings.HasPrefix(key, prefix) {
			if out == "!missing" {
				return "", errors.New(name + ": " + execx.ErrNotFound.Error())
			}
			if strings.HasPrefix(out, "!fail ") {
				return "", errors.New(name + " failed:\n" + strings.TrimPrefix(out, "!fail "))
			}
			return out, nil
		}
	}
	return "", errors.New("unexpected " + key)
}

func goodTools() fakeTools {
	return fakeTools{
		"git --version":          "git version 2.43.0\n",
		"docker --version":       "Docker version 29.9.2, build abc\n",
		"docker info":            "29.9.2 linux\n",
		"docker compose version": "5.6.1\n",
		"docker-compose version": "!missing",
		"reg query":              "    LongPathsEnabled    REG_DWORD    0x1\n",
	}
}

func doctor(t *testing.T, goos string, tools fakeTools, projects ...string) DoctorReport {
	t.Helper()
	return RunDoctor(context.Background(), DoctorEnv{GOOS: goos, DataDir: t.TempDir(), Projects: projects, Run: tools.run})
}

func find(t *testing.T, r DoctorReport, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, r.Checks)
	return Check{}
}

func TestDoctorAllGood(t *testing.T) {
	r := doctor(t, "linux", goodTools())
	if !r.OK {
		t.Fatalf("report = %+v", r)
	}
	for _, c := range r.Checks {
		if c.Status != StatusOK {
			t.Errorf("%s: %s %s", c.ID, c.Status, c.Detail)
		}
	}
}

func TestDoctorGitTooOldAndMissing(t *testing.T) {
	tools := goodTools()
	tools["git --version"] = "git version 2.11.0\n"
	r := doctor(t, "linux", tools)
	c := find(t, r, "git")
	if r.OK || c.Status != StatusFail || !strings.Contains(c.Detail, "2.17.0") || c.Fix == "" {
		t.Errorf("old git: %+v (ok=%v)", c, r.OK)
	}

	tools["git --version"] = "!missing"
	if c := find(t, doctor(t, "linux", tools), "git"); c.Status != StatusFail || !strings.Contains(c.Detail, "not installed") {
		t.Errorf("missing git: %+v", c)
	}

	tools["git --version"] = "git version 2.39.3 (Apple Git-145)\n"
	if c := find(t, doctor(t, "darwin", tools), "git"); c.Status != StatusOK {
		t.Errorf("Apple git: %+v", c)
	}
	tools["git --version"] = "git version 2.45.0.windows.1\n"
	if c := find(t, doctor(t, "windows", tools), "git"); c.Status != StatusOK {
		t.Errorf("Git for Windows: %+v", c)
	}
}

func TestDoctorComposeVersions(t *testing.T) {
	tests := []struct {
		out    string
		status string
	}{
		{"2.24.4\n", StatusOK},
		{"v2.24.3\n", StatusFail},
		{"2.20.2-desktop.1\n", StatusFail},
		{"v2.29.1-desktop.1\n", StatusOK},
		{"garbage\n", StatusWarn},
	}
	for _, tt := range tests {
		tools := goodTools()
		tools["docker compose version"] = tt.out
		r := doctor(t, "linux", tools)
		c := find(t, r, "docker-compose")
		if c.Status != tt.status {
			t.Errorf("%q: status %s (%s), want %s", tt.out, c.Status, c.Detail, tt.status)
		}
		if tt.status == StatusFail && (!strings.Contains(c.Detail, "2.24.4") || c.Fix == "" || r.OK) {
			t.Errorf("%q: the message must give the minimum and a fix: %+v", tt.out, c)
		}
	}
}

func TestDoctorComposeMissingMentionsStandalone(t *testing.T) {
	tools := goodTools()
	tools["docker compose version"] = "!fail unknown command: docker compose"
	tools["docker-compose version"] = "1.29.2\n"
	c := find(t, doctor(t, "linux", tools), "docker-compose")
	if c.Status != StatusFail || !strings.Contains(c.Detail, "standalone") || !strings.Contains(c.Fix, "2.24.4") {
		t.Errorf("check = %+v", c)
	}
}

func TestDoctorNoDocker(t *testing.T) {
	tools := goodTools()
	tools["docker --version"] = "!missing"
	r := doctor(t, "windows", tools)
	if r.OK {
		t.Fatal("must fail without docker")
	}
	for _, id := range []string{"docker", "docker-daemon", "docker-compose"} {
		if c := find(t, r, id); c.Status != StatusFail || !strings.Contains(c.Fix, "Docker Desktop") {
			t.Errorf("%s: %+v", id, c)
		}
	}
}

func TestDoctorDaemonDownAndWindowsContainers(t *testing.T) {
	tools := goodTools()
	tools["docker info"] = "!fail Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
	c := find(t, doctor(t, "linux", tools), "docker-daemon")
	if c.Status != StatusFail || !strings.Contains(c.Detail, "Cannot connect") || !strings.Contains(c.Fix, "systemctl") {
		t.Errorf("daemon down: %+v", c)
	}
	if c := find(t, doctor(t, "darwin", tools), "docker-daemon"); !strings.Contains(c.Fix, "Docker Desktop") {
		t.Errorf("mac fix: %+v", c)
	}

	tools["docker info"] = "29.9.2 windows\n"
	r := doctor(t, "windows", tools)
	if c := find(t, r, "docker-os"); r.OK || c.Status != StatusFail || !strings.Contains(c.Fix, "Linux containers") {
		t.Errorf("windows containers: %+v", c)
	}
}

func TestDoctorDataDirAndStateFiles(t *testing.T) {
	dir := t.TempDir()
	env := DoctorEnv{GOOS: "linux", DataDir: dir, Run: goodTools().run}

	// A data directory that does not exist yet is created.
	env.DataDir = filepath.Join(dir, "new", "theo")
	r := RunDoctor(context.Background(), env)
	if c := find(t, r, "data-dir"); c.Status != StatusOK {
		t.Errorf("new dir: %+v", c)
	}

	// A path under a file cannot be a directory.
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	env.DataDir = filepath.Join(file, "theo")
	r = RunDoctor(context.Background(), env)
	if c := find(t, r, "data-dir"); r.OK || c.Status != StatusFail || !strings.Contains(c.Fix, "THEO_HOME") {
		t.Errorf("unusable dir: %+v", c)
	}

	// Valid, corrupt and unreadable-by-content state files.
	env.DataDir = t.TempDir()
	os.WriteFile(filepath.Join(env.DataDir, "registry.json"), []byte(`{"version":1}`), 0o644)
	os.WriteFile(filepath.Join(env.DataDir, "history.jsonl"), []byte("{\"a\":1}\n\n{\"b\":2}\n"), 0o644)
	if c := find(t, RunDoctor(context.Background(), env), "state-files"); c.Status != StatusOK || !strings.Contains(c.Detail, "2 readable") {
		t.Errorf("good state: %+v", c)
	}
	os.WriteFile(filepath.Join(env.DataDir, "ports.json"), []byte(`{oops`), 0o644)
	os.WriteFile(filepath.Join(env.DataDir, "history.jsonl"), []byte("{\"a\":1}\nnot json\n"), 0o644)
	r = RunDoctor(context.Background(), env)
	c := find(t, r, "state-files")
	if r.OK || c.Status != StatusFail || !strings.Contains(c.Detail, "ports.json") || !strings.Contains(c.Detail, "history.jsonl: line 2") {
		t.Errorf("corrupt state: %+v", c)
	}
}

func TestDoctorPlatformNotes(t *testing.T) {
	// macOS: a project outside the shared folders warns, and warnings do not fail.
	r := doctor(t, "darwin", goodTools(), "/Users/me/app", "/opt/code/app")
	c := find(t, r, "macos-file-sharing")
	if !r.OK || c.Status != StatusWarn || !strings.Contains(c.Detail, "/opt/code/app") || strings.Contains(c.Detail, "/Users/me/app") {
		t.Errorf("mac: %+v (ok=%v)", c, r.OK)
	}
	if c := find(t, doctor(t, "darwin", goodTools(), "/Users/me/app"), "macos-file-sharing"); c.Status != StatusInfo {
		t.Errorf("mac, all shared: %+v", c)
	}

	// Windows: long paths disabled warns; WSL 2 advice is a note.
	tools := goodTools()
	tools["reg query"] = "    LongPathsEnabled    REG_DWORD    0x0\n"
	r = doctor(t, "windows", tools)
	if c := find(t, r, "windows-long-paths"); !r.OK || c.Status != StatusWarn || !strings.Contains(c.Fix, "LongPathsEnabled") {
		t.Errorf("windows long paths: %+v", c)
	}
	if c := find(t, r, "windows-wsl2"); c.Status != StatusInfo {
		t.Errorf("wsl2: %+v", c)
	}
	if c := find(t, doctor(t, "windows", goodTools()), "windows-long-paths"); c.Status != StatusOK {
		t.Errorf("long paths enabled: %+v", c)
	}

	// Linux has no notes.
	for _, c := range doctor(t, "linux", goodTools()).Checks {
		if strings.HasPrefix(c.ID, "windows") || strings.HasPrefix(c.ID, "macos") {
			t.Errorf("linux got %s", c.ID)
		}
	}
}
