package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Target identifies a Compose project to operate on: the directory with its
// Compose files, the project name and the env files to use instead of the
// directory's .env.
type Target struct {
	Dir      string
	Name     string
	EnvFiles []string
}

func (t Target) args(profiles []string, sub ...string) []string {
	args := []string{"compose", "-p", t.Name}
	for _, f := range t.EnvFiles {
		args = append(args, "--env-file", f)
	}
	for _, p := range profiles {
		args = append(args, "--profile", p)
	}
	return append(args, sub...)
}

// Container is a container of a Compose project, as `docker compose ps`
// reports it.
type Container struct {
	Name       string      `json:"Name"`
	Service    string      `json:"Service"`
	State      string      `json:"State"`
	Health     string      `json:"Health"`
	Status     string      `json:"Status"`
	ExitCode   int         `json:"ExitCode"`
	Publishers []Publisher `json:"Publishers"`
}

// Publisher is a published port of a container.
type Publisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

// Stack is a Compose project known to Docker, as `docker compose ls`
// reports it.
type Stack struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"` // comma-separated absolute paths
}

// UpOptions are the options of Up.
type UpOptions struct {
	Build    bool
	Profiles []string
}

// LogOptions are the options of Logs.
type LogOptions struct {
	Follow   bool
	Tail     int // lines per container; 0 means all
	Services []string
}

// Up creates and starts the project's containers in the background.
func (Compose) Up(ctx context.Context, t Target, opts UpOptions, stdout, stderr io.Writer) error {
	sub := []string{"up", "--detach"}
	if opts.Build {
		sub = append(sub, "--build")
	}
	return execx.Stream(ctx, t.Dir, stdout, stderr, "docker", t.args(opts.Profiles, sub...)...)
}

// Down stops and removes the project's containers and networks. Volumes
// are always kept: Down never passes --volumes.
func (Compose) Down(ctx context.Context, t Target, stdout, stderr io.Writer) error {
	return execx.Stream(ctx, t.Dir, stdout, stderr, "docker", t.args([]string{"*"}, "down")...)
}

// Restart restarts the project's containers.
func (Compose) Restart(ctx context.Context, t Target, stdout, stderr io.Writer) error {
	return execx.Stream(ctx, t.Dir, stdout, stderr, "docker", t.args([]string{"*"}, "restart")...)
}

// Logs writes the logs of the project's containers.
func (Compose) Logs(ctx context.Context, t Target, opts LogOptions, stdout, stderr io.Writer) error {
	sub := []string{"logs"}
	if opts.Follow {
		sub = append(sub, "--follow")
	}
	if opts.Tail > 0 {
		sub = append(sub, "--tail", strconv.Itoa(opts.Tail))
	}
	sub = append(sub, opts.Services...)
	return execx.Stream(ctx, t.Dir, stdout, stderr, "docker", t.args([]string{"*"}, sub...)...)
}

// PS returns the project's containers, including stopped ones.
func (Compose) PS(ctx context.Context, t Target) ([]Container, error) {
	out, err := execx.Run(ctx, t.Dir, "docker", t.args([]string{"*"}, "ps", "--all", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	return ParsePS(out)
}

// Stacks returns every Compose project known to Docker, running or not.
func (Compose) Stacks(ctx context.Context) ([]Stack, error) {
	out, err := execx.Run(ctx, "", "docker", "compose", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	var stacks []Stack
	if err := json.Unmarshal(out, &stacks); err != nil {
		return nil, fmt.Errorf("unexpected docker compose ls output: %w", err)
	}
	return stacks, nil
}

// ParsePS parses the output of `docker compose ps --format json`, which is
// one JSON object per line in recent versions and a JSON array in older
// ones.
func ParsePS(out []byte) ([]Container, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	if out[0] == '[' {
		var list []Container
		if err := json.Unmarshal(out, &list); err != nil {
			return nil, fmt.Errorf("unexpected docker compose ps output: %w", err)
		}
		return list, nil
	}
	var list []Container
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c Container
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("unexpected docker compose ps output: %w", err)
		}
		list = append(list, c)
	}
	return list, sc.Err()
}
