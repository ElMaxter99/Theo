package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Labels Compose puts on every container it creates.
const (
	LabelService = "com.docker.compose.service"
)

// inspectTemplate asks `docker inspect` for only what Container needs, one
// JSON object per line.
const inspectTemplate = `{"name":{{json .Name}},` +
	`"project":{{json (index .Config.Labels "` + LabelProject + `")}},` +
	`"service":{{json (index .Config.Labels "` + LabelService + `")}},` +
	`"state":{{json .State.Status}},` +
	`"exit":{{json .State.ExitCode}},` +
	`"health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}""{{end}},` +
	`"ports":{{json .NetworkSettings.Ports}}}`

// AllContainers returns the containers of every Compose project Docker has,
// running or not, by the lowercase name of the project. It is the same
// information `docker compose ps --all` gives for each project, read with two
// commands for all of them (the list of container ids by Compose label, then
// one `docker inspect`) instead of one `docker compose ps` per instance.
func (Compose) AllContainers(ctx context.Context) (map[string][]Container, error) {
	for attempt := 0; ; attempt++ {
		ids, err := execx.Run(ctx, "", "docker", "ps", "--all", "--no-trunc", "--filter", "label="+LabelProject, "--format", "{{.ID}}")
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(string(ids))
		if len(fields) == 0 {
			return map[string][]Container{}, nil
		}
		args := append([]string{"inspect", "--type", "container", "--format", inspectTemplate}, fields...)
		out, err := execx.Run(ctx, "", "docker", args...)
		if err != nil {
			// A container removed between the two commands makes inspect fail
			// for all of them; the list is stable a moment later.
			if attempt == 0 && strings.Contains(err.Error(), "No such") && ctx.Err() == nil {
				continue
			}
			return nil, err
		}
		return ParseInspect(out)
	}
}

// ParseInspect parses the output of `docker inspect` with inspectTemplate
// into the containers of each Compose project (key: lowercase project name).
func ParseInspect(out []byte) (map[string][]Container, error) {
	byProject := map[string][]Container{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Name    string                                         `json:"name"`
			Project string                                         `json:"project"`
			Service string                                         `json:"service"`
			State   string                                         `json:"state"`
			Exit    int                                            `json:"exit"`
			Health  string                                         `json:"health"`
			Ports   map[string][]struct{ HostIp, HostPort string } `json:"ports"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("unexpected docker inspect output: %w", err)
		}
		if raw.Project == "" {
			continue
		}
		c := Container{Name: strings.TrimPrefix(raw.Name, "/"), Service: raw.Service, State: raw.State, Health: raw.Health, ExitCode: raw.Exit, Status: raw.State}
		keys := make([]string, 0, len(raw.Ports))
		for k := range raw.Ports {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			portStr, proto, _ := strings.Cut(k, "/")
			target, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}
			bindings := raw.Ports[k]
			if len(bindings) == 0 {
				c.Publishers = append(c.Publishers, Publisher{TargetPort: target, Protocol: proto})
				continue
			}
			for _, b := range bindings {
				pub, _ := strconv.Atoi(b.HostPort)
				c.Publishers = append(c.Publishers, Publisher{URL: b.HostIp, TargetPort: target, PublishedPort: pub, Protocol: proto})
			}
		}
		key := strings.ToLower(raw.Project)
		byProject[key] = append(byProject[key], c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, list := range byProject {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	return byProject, nil
}
