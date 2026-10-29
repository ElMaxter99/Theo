package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Docker labels that Compose puts on the volumes it creates.
const (
	LabelProject = "com.docker.compose.project"
	LabelVolume  = "com.docker.compose.volume"
)

// DockerVolume is a Docker volume.
type DockerVolume struct {
	Name   string
	Driver string
	Labels map[string]string
}

// Volumes lists the volumes that Compose created for a project, by its
// labels. Volumes declared as external, which Compose never creates, are not
// among them.
func (Compose) Volumes(ctx context.Context, project string) ([]DockerVolume, error) {
	out, err := execx.Run(ctx, "", "docker", "volume", "ls", "--filter", "label="+LabelProject+"="+project, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return ParseVolumes(out)
}

// ParseVolumes parses the output of `docker volume ls --format {{json .}}`.
func ParseVolumes(out []byte) ([]DockerVolume, error) {
	var list []DockerVolume
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Name   string
			Driver string
			Labels string
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("unexpected docker volume ls output: %w", err)
		}
		v := DockerVolume{Name: raw.Name, Driver: raw.Driver, Labels: map[string]string{}}
		for _, kv := range strings.Split(raw.Labels, ",") {
			if k, val, ok := strings.Cut(kv, "="); ok {
				v.Labels[k] = val
			}
		}
		list = append(list, v)
	}
	return list, sc.Err()
}

// VolumeUsers returns the names of the containers, running or stopped, that
// mount the volume.
func (Compose) VolumeUsers(ctx context.Context, volume string) ([]string, error) {
	out, err := execx.Run(ctx, "", "docker", "ps", "--all", "--filter", "volume="+volume, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// RemoveVolume deletes a volume and its data. It never forces: Docker
// refuses a volume that a container still uses.
func (Compose) RemoveVolume(ctx context.Context, volume string) error {
	_, err := execx.Run(ctx, "", "docker", "volume", "rm", "--", volume)
	return err
}

// LabelManaged marks the Docker networks Theo creates.
const LabelManaged = "com.theo.managed"

// EnsureNetwork creates a Docker network for sharing services between
// instances if it does not exist, labelled as Theo's. It reports whether it
// created it.
func (Compose) EnsureNetwork(ctx context.Context, name string) (bool, error) {
	if _, err := execx.Run(ctx, "", "docker", "network", "inspect", name); err == nil {
		return false, nil
	}
	if _, err := execx.Run(ctx, "", "docker", "network", "create", "--label", LabelManaged+"=true", name); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveNetwork removes a network Theo created. Docker refuses one that
// still has containers attached, and so does Theo: there is no force.
func (Compose) RemoveNetwork(ctx context.Context, name string) error {
	out, err := execx.Run(ctx, "", "docker", "network", "inspect", "--format", `{{index .Labels "`+LabelManaged+`"}}`, name)
	if err != nil {
		return nil // already gone
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("network %s was not created by Theo; not removing it", name)
	}
	_, err = execx.Run(ctx, "", "docker", "network", "rm", name)
	return err
}
