package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Check statuses. Only StatusFail makes the doctor fail; a warning is
// something worth knowing that does not stop Theo.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
	StatusInfo = "info"
)

// MinGitVersion is the oldest Git with `git worktree remove`.
var MinGitVersion = compose.Version{Major: 2, Minor: 17, Patch: 0}

// Check is the result of one doctor check.
type Check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"` // what to do, when the status is warn or fail
}

// DoctorReport is the result of Doctor.
type DoctorReport struct {
	OK       bool    `json:"ok"` // no check failed
	Platform string  `json:"platform"`
	DataDir  string  `json:"data_dir"`
	Checks   []Check `json:"checks"`
}

// DoctorEnv is what the doctor looks at. Production code uses
// RealDoctorEnv; tests give fakes.
type DoctorEnv struct {
	GOOS    string
	DataDir string
	// Projects are the paths of the registered projects, for notes about
	// where they live.
	Projects []string
	// Run runs a tool and returns its standard output. It returns an error
	// wrapping execx.ErrNotFound when the tool is not installed.
	Run func(ctx context.Context, name string, args ...string) (string, error)
}

// RealDoctorEnv returns a DoctorEnv for this machine.
func RealDoctorEnv(dataDir string, projects []string) DoctorEnv {
	return DoctorEnv{
		GOOS: runtime.GOOS, DataDir: dataDir, Projects: projects,
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := execx.Run(ctx, "", name, args...)
			return string(out), err
		},
	}
}

// Doctor checks that what Theo needs works on this machine.
func (e *Engine) Doctor(ctx context.Context) DoctorReport {
	var projects []string
	if ps, err := e.store.Load(); err == nil {
		for _, p := range ps {
			projects = append(projects, p.Path)
		}
	}
	return RunDoctor(ctx, RealDoctorEnv(e.dataDir, projects))
}

// RunDoctor runs every check. A check never stops the others.
func RunDoctor(ctx context.Context, env DoctorEnv) DoctorReport {
	r := DoctorReport{Platform: env.GOOS, DataDir: env.DataDir}
	add := func(c Check) { r.Checks = append(r.Checks, c) }

	add(checkGit(ctx, env))
	dockerCheck, haveDocker := checkDocker(ctx, env)
	add(dockerCheck)
	if haveDocker {
		daemon, osType := checkDaemon(ctx, env)
		add(daemon)
		if osType != "" {
			add(checkEngineOS(env, osType))
		}
		add(checkComposePlugin(ctx, env))
	} else {
		add(Check{ID: "docker-daemon", Name: "Docker daemon", Status: StatusFail, Detail: "not checked: docker is not installed", Fix: dockerInstallHint(env.GOOS)})
		add(Check{ID: "docker-compose", Name: "docker compose plugin", Status: StatusFail, Detail: "not checked: docker is not installed", Fix: dockerInstallHint(env.GOOS)})
	}
	add(checkDataDir(env))
	add(checkStateFiles(env))
	for _, c := range platformNotes(ctx, env) {
		add(c)
	}

	r.OK = true
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			r.OK = false
		}
	}
	return r
}

func dockerInstallHint(goos string) string {
	switch goos {
	case "windows", "darwin":
		return "install Docker Desktop: https://docs.docker.com/desktop/"
	}
	return "install Docker Engine and the Compose plugin: https://docs.docker.com/engine/install/"
}

func checkGit(ctx context.Context, env DoctorEnv) Check {
	c := Check{ID: "git", Name: "git"}
	out, err := env.Run(ctx, "git", "--version")
	if err != nil {
		c.Status = StatusFail
		c.Detail = "git is not installed or not in PATH"
		c.Fix = "install Git 2.17 or later: https://git-scm.com/downloads"
		return c
	}
	line := strings.TrimSpace(out)
	v, err := compose.ParseVersion(line)
	if err != nil {
		c.Status, c.Detail = StatusWarn, "found "+line+", but its version could not be read; Theo needs 2.17 or later"
		return c
	}
	c.Detail = line
	if !v.AtLeast(MinGitVersion) {
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("%s: too old, `git worktree remove` needs %s or later", line, MinGitVersion)
		c.Fix = "upgrade Git: https://git-scm.com/downloads"
		return c
	}
	c.Status = StatusOK
	return c
}

func checkDocker(ctx context.Context, env DoctorEnv) (Check, bool) {
	c := Check{ID: "docker", Name: "docker"}
	out, err := env.Run(ctx, "docker", "--version")
	if err != nil {
		c.Status = StatusFail
		c.Detail = "docker is not installed or not in PATH"
		c.Fix = dockerInstallHint(env.GOOS)
		return c, false
	}
	c.Status, c.Detail = StatusOK, strings.TrimSpace(out)
	return c, true
}

// checkDaemon asks the daemon for its version and OS type in one call.
func checkDaemon(ctx context.Context, env DoctorEnv) (Check, string) {
	c := Check{ID: "docker-daemon", Name: "Docker daemon"}
	out, err := env.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}} {{.OSType}}")
	fields := strings.Fields(out)
	if err != nil || len(fields) < 2 {
		c.Status = StatusFail
		c.Detail = "cannot reach the Docker daemon"
		if err != nil {
			c.Detail += ": " + lastLine(err.Error())
		}
		switch env.GOOS {
		case "linux":
			c.Fix = "start it (`sudo systemctl start docker`) and, if the error is about permissions, add your user to the docker group (`sudo usermod -aG docker $USER`, then log in again)"
		default:
			c.Fix = "start Docker Desktop and wait until it says it is running"
		}
		return c, ""
	}
	c.Status, c.Detail = StatusOK, "engine "+fields[0]
	return c, fields[1]
}

func checkEngineOS(env DoctorEnv, osType string) Check {
	c := Check{ID: "docker-os", Name: "Docker containers", Detail: osType + " containers"}
	if osType == "linux" {
		c.Status = StatusOK
		return c
	}
	c.Status = StatusFail
	c.Detail = osType + " containers: Theo's projects and generated files assume Linux containers"
	if env.GOOS == "windows" {
		c.Fix = "switch Docker Desktop to Linux containers (tray icon > \"Switch to Linux containers...\"), or use the WSL 2 backend"
	} else {
		c.Fix = "use a Docker engine that runs Linux containers"
	}
	return c
}

func checkComposePlugin(ctx context.Context, env DoctorEnv) Check {
	c := Check{ID: "docker-compose", Name: "docker compose plugin"}
	out, err := env.Run(ctx, "docker", "compose", "version", "--short")
	if err != nil {
		c.Status = StatusFail
		c.Detail = "the `docker compose` plugin is not available"
		if _, lerr := env.Run(ctx, "docker-compose", "version", "--short"); lerr == nil {
			c.Detail += " (the old standalone docker-compose is not supported)"
		}
		c.Fix = fmt.Sprintf("install the Compose plugin %s or later: https://docs.docker.com/compose/install/", compose.MinVersion)
		return c
	}
	v, err := compose.ParseVersion(out)
	if err != nil {
		c.Status = StatusWarn
		c.Detail = "found " + strings.TrimSpace(out) + ", but its version could not be read; Theo needs " + compose.MinVersion.String() + " or later"
		return c
	}
	c.Detail = "Docker Compose " + v.String()
	if !v.AtLeast(compose.MinVersion) {
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("Docker Compose %s is too old: Theo needs %s or later (the !override and !reset tags of the files it generates)", v, compose.MinVersion)
		switch env.GOOS {
		case "linux":
			c.Fix = "upgrade the plugin: update the docker-compose-plugin package (`sudo apt-get install --only-upgrade docker-compose-plugin`, or your distribution's equivalent), or follow https://docs.docker.com/compose/install/linux/"
		default:
			c.Fix = "upgrade Docker Desktop to its latest release (it bundles Compose); " + compose.MinVersion.String() + " or later is needed"
		}
		return c
	}
	c.Status = StatusOK
	return c
}

func checkDataDir(env DoctorEnv) Check {
	c := Check{ID: "data-dir", Name: "Theo data directory", Detail: env.DataDir}
	if env.DataDir == "" {
		c.Status = StatusFail
		c.Detail = "no data directory"
		c.Fix = "set " + registry.EnvHome + " to a directory you can write to"
		return c
	}
	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		c.Status = StatusFail
		c.Detail = env.DataDir + ": cannot create it: " + lastLine(err.Error())
		c.Fix = "fix the permissions, or set " + registry.EnvHome + " to a directory you can write to"
		return c
	}
	f, err := os.CreateTemp(env.DataDir, "doctor-*.tmp")
	if err != nil {
		c.Status = StatusFail
		c.Detail = env.DataDir + ": not writable: " + lastLine(err.Error())
		c.Fix = "fix the permissions, or set " + registry.EnvHome + " to a directory you can write to"
		return c
	}
	f.Close()
	os.Remove(f.Name())
	c.Status = StatusOK
	return c
}

// stateFiles are the files Theo keeps in its data directory, and whether each
// line (jsonl) or the whole file (json) must be valid JSON.
var stateFiles = []struct {
	name  string
	lines bool
}{
	{"registry.json", false}, {"ports.json", false}, {"instances.json", false}, {"history.jsonl", true},
}

func checkStateFiles(env DoctorEnv) Check {
	c := Check{ID: "state-files", Name: "Theo state files"}
	var problems []string
	found := 0
	for _, f := range stateFiles {
		path := filepath.Join(env.DataDir, f.name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		found++
		if err != nil {
			problems = append(problems, f.name+": cannot be read: "+lastLine(err.Error()))
			continue
		}
		if f.lines {
			for i, line := range bytes.Split(data, []byte("\n")) {
				if len(bytes.TrimSpace(line)) > 0 && !json.Valid(line) {
					problems = append(problems, fmt.Sprintf("%s: line %d is not valid JSON", f.name, i+1))
					break
				}
			}
		} else if !json.Valid(data) {
			problems = append(problems, f.name+": not valid JSON")
		}
	}
	switch {
	case len(problems) > 0:
		c.Status = StatusFail
		c.Detail = strings.Join(problems, "; ")
		c.Fix = "fix or move the file in " + env.DataDir + " (Theo does not delete it); a file you move away is recreated empty the next time Theo saves"
	case found == 0:
		c.Status, c.Detail = StatusOK, "none yet (created when you register a project)"
	default:
		c.Status, c.Detail = StatusOK, fmt.Sprintf("%d readable", found)
	}
	return c
}

// macShared are the paths Docker Desktop for Mac shares with containers by
// default.
var macShared = []string{"/Users", "/Volumes", "/private", "/tmp", "/var/folders"}

// platformNotes returns the notes that depend on the operating system.
func platformNotes(ctx context.Context, env DoctorEnv) []Check {
	switch env.GOOS {
	case "darwin":
		c := Check{ID: "macos-file-sharing", Name: "macOS file sharing", Status: StatusInfo,
			Detail: "Docker Desktop can only bind-mount folders it shares (by default " + strings.Join(macShared, ", ") + "); worktrees outside them start empty in the container"}
		var outside []string
		for _, p := range env.Projects {
			ok := false
			for _, s := range macShared {
				if p == s || strings.HasPrefix(p, s+"/") {
					ok = true
				}
			}
			if !ok {
				outside = append(outside, p)
			}
		}
		if len(outside) > 0 {
			c.Status = StatusWarn
			c.Detail = "projects outside the folders Docker Desktop shares by default: " + strings.Join(outside, ", ")
			c.Fix = "add them in Docker Desktop > Settings > Resources > File sharing, or keep the project (and its worktrees) under your home directory"
		}
		return []Check{c}
	case "windows":
		var out []Check
		long := Check{ID: "windows-long-paths", Name: "Windows long paths", Status: StatusInfo,
			Detail: "worktrees repeat the project's path; deep trees (node_modules) can pass the 260-character limit"}
		if res, err := env.Run(ctx, "reg", "query", `HKLM\SYSTEM\CurrentControlSet\Control\FileSystem`, "/v", "LongPathsEnabled"); err == nil {
			if strings.Contains(res, "0x1") {
				long.Status, long.Detail = StatusOK, "enabled"
			} else {
				long.Status = StatusWarn
				long.Detail = "long paths are disabled: deep worktrees can fail with \"filename too long\""
				long.Fix = `enable them (an administrator PowerShell: New-ItemProperty -Path HKLM:\SYSTEM\CurrentControlSet\Control\FileSystem -Name LongPathsEnabled -Value 1 -PropertyType DWORD -Force) and run git config --global core.longpaths true`
			}
		}
		if len(env.DataDir) > 120 {
			long.Status = StatusWarn
			long.Detail += "; the data directory path is long (" + env.DataDir + ")"
			if long.Fix == "" {
				long.Fix = "set THEO_HOME to a shorter path"
			}
		}
		out = append(out, long)
		out = append(out, Check{ID: "windows-wsl2", Name: "Windows and WSL 2", Status: StatusInfo,
			Detail: "bind mounts and file watching are much faster when the project lives in the WSL 2 file system (\\\\wsl$\\...) than under C:\\; use the WSL 2 backend of Docker Desktop"})
		return out
	}
	return nil
}

// lastLine returns the last non-empty line of s, which is where tools put
// the actual reason.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
