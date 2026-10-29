// Package compose is Theo's adapter for the docker compose command-line
// tool. It reads the resolved configuration of a project with
// `docker compose config` and never starts, stops or changes anything.
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Project is the part of the resolved Compose model that Theo uses.
type Project struct {
	Name     string             `json:"name"`
	Services map[string]Service `json:"services"`
}

// Service is a service of the resolved Compose model.
type Service struct {
	Image         string                     `json:"image"`
	ContainerName string                     `json:"container_name"`
	Build         *Build                     `json:"build"`
	Ports         []Port                     `json:"ports"`
	Volumes       []Volume                   `json:"volumes"`
	DependsOn     map[string]json.RawMessage `json:"depends_on"`
	Networks      map[string]json.RawMessage `json:"networks"`
	Profiles      []string                   `json:"profiles"`
}

// Build is the build section of a service.
type Build struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}

// Port is a port mapping. Published is empty when Docker picks the host port.
type Port struct {
	HostIP    string `json:"host_ip,omitempty"`
	Target    uint32 `json:"target"`
	Published string `json:"published,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// Volume is a mount of a service: a named volume, a bind mount or a tmpfs.
type Volume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Compose runs docker compose commands.
type Compose struct{}

// Config returns the resolved configuration of the Compose project in dir,
// as `docker compose config` sees it with the user's own files and
// environment. All profiles are enabled so that every service is listed.
// envFiles, when given, replace the project's default .env file.
func (Compose) Config(ctx context.Context, dir string, envFiles []string) (*Project, error) {
	args := []string{"compose", "--profile", "*"}
	for _, f := range envFiles {
		args = append(args, "--env-file", f)
	}
	args = append(args, "config", "--format", "json")
	out, err := execx.Run(ctx, dir, "docker", args...)
	if err != nil {
		return nil, err
	}
	return ParseConfig(out)
}

// ParseConfig parses the output of `docker compose config --format json`.
func ParseConfig(out []byte) (*Project, error) {
	var p Project
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("unexpected docker compose config output: %w", err)
	}
	return &p, nil
}

// SortedKeys returns the keys of m in alphabetical order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
