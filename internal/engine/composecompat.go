package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// versioner is implemented by Compose adapters that can tell their version.
// It is optional so that the Compose interface stays as it is.
type versioner interface {
	Version(ctx context.Context) (compose.Version, error)
}

// requireOverlayCompose fails when the docker compose plugin is too old for
// the !override and !reset tags of the files Theo generates, and says what to
// do. A version that cannot be read is not an error here: the generated file
// is then validated by `docker compose config` anyway.
func (e *Engine) requireOverlayCompose(ctx context.Context) error {
	v, ok := e.compose.(versioner)
	if !ok {
		return nil
	}
	ver, err := v.Version(ctx)
	if err != nil || ver.AtLeast(compose.MinVersion) {
		return nil
	}
	return compose.ErrTooOld(ver)
}

// dotenv reads KEY=VALUE lines of an env file: comments and blank lines are
// skipped, "export " and one pair of quotes are removed. It does not
// interpolate. It is meant for the few variables Theo reads itself
// (COMPOSE_FILE, COMPOSE_PROFILES, COMPOSE_PATH_SEPARATOR).
func dotenv(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\xef\xbb\xbf"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if n := len(v); n >= 2 && (v[0] == '"' || v[0] == '\'') && v[n-1] == v[0] {
			v = v[1 : n-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out
}

// composeVar returns the value Compose sees for one of its own settings
// (COMPOSE_FILE and the like) in the instance: the process environment wins
// over the env files, and a later --env-file over an earlier one. Without
// --env-file, Compose reads the .env of the project directory.
func composeVar(inst *Instance, name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	files := inst.envFiles
	if len(files) == 0 {
		files = []string{filepath.Join(inst.Workspace, ".env")}
	}
	value := ""
	for _, f := range files {
		if v, ok := dotenv(f)[name]; ok {
			value = v
		}
	}
	return value
}

// composeFileList returns the files named by COMPOSE_FILE, or nil when it is
// not set. Relative paths are relative to the project directory.
func composeFileList(inst *Instance) []string {
	raw := composeVar(inst, "COMPOSE_FILE")
	if raw == "" {
		return nil
	}
	sep := composeVar(inst, "COMPOSE_PATH_SEPARATOR")
	if sep == "" {
		sep = string(os.PathListSeparator) // ":" and, on Windows, ";"
	}
	var files []string
	for _, f := range strings.Split(raw, sep) {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if !filepath.IsAbs(f) {
			f = filepath.Join(inst.Workspace, f)
		}
		files = append(files, filepath.Clean(f))
	}
	return files
}

// composeProfiles returns the profiles COMPOSE_PROFILES enables for the
// instance, which Compose adds to the ones given with --profile.
func composeProfiles(inst *Instance) []string {
	var out []string
	for _, p := range strings.Split(composeVar(inst, "COMPOSE_PROFILES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// baseComposeFiles returns the Compose files Compose would load for the
// instance, which a generated file must name explicitly to be added to them
// (passing any -f turns Compose's own search off). They come from
// COMPOSE_FILE when it is set, and from the usual search otherwise
// (compose.yaml and its override file, and the docker-compose.* names).
//
// Theo does not trust its own reading: it asks Compose for the resolved
// configuration both ways and refuses to continue when they differ, so a
// generated file can never silently drop a file of yours.
func (e *Engine) baseComposeFiles(ctx context.Context, inst *Instance) ([]string, error) {
	files := composeFileList(inst)
	if len(files) == 0 {
		files = e.baseFiles(inst.Workspace)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("theo needs a compose.yaml (or docker-compose.yml) in %s to add its generated file, and found none; if your Compose files live elsewhere, set COMPOSE_FILE", inst.Workspace)
	}
	t := inst.target()
	t.Files = nil
	own, err := e.compose.ConfigText(ctx, t)
	if err != nil {
		return nil, err
	}
	t.Files = files
	mine, err := e.compose.ConfigText(ctx, t)
	if err != nil {
		return nil, err
	}
	if string(own) != string(mine) {
		return nil, fmt.Errorf("theo could not work out which Compose files %s loads (it would use %s) and the result differs from Compose's own. Name them in order with COMPOSE_FILE (for example in the .env), or free the ports by hand", inst.Workspace, strings.Join(files, ", "))
	}
	return files, nil
}

// checkOverlay verifies that the resolved configuration applies what the
// overlay asked for besides ports: the variables set with `theo env` and the
// removal of fixed container names.
func checkOverlay(o compose.Overlay, cfg *compose.Project) error {
	for _, svc := range o.ResetContainerName {
		if cfg.Services[svc].ContainerName != "" {
			return fmt.Errorf("the resolved configuration still has a container_name for %s", svc)
		}
	}
	for svc, env := range o.Environment {
		got := cfg.Services[svc].Environment
		for name, want := range env {
			// `compose config` prints a literal $ as $$ so that its output can be fed back.
			if v, ok := got[name]; !ok || v == nil || (*v != want && *v != strings.ReplaceAll(want, "$", "$$")) {
				return fmt.Errorf("the resolved configuration does not set %s in service %s", name, svc)
			}
		}
	}
	return nil
}

// SharedImageBuilds returns the services of the instance (service -> image)
// that build an image with a fixed name when the project has more than one
// worktree: a `build:` together with an `image:` that does not depend on the
// Compose project name. All instances build into the same tag, so building
// one overwrites the image the others run. Without `image:`, Compose names
// the image <project>-<service> and each instance has its own, and so does an
// image such as myapp-${COMPOSE_PROJECT_NAME}. Theo tells them apart by
// resolving the configuration again under another project name: an image
// that changes with it is per instance. The result is empty when there is
// nothing to warn about.
func (e *Engine) SharedImageBuilds(ctx context.Context, inst *Instance) (map[string]string, error) {
	if inst.siblings == 0 {
		return nil, nil
	}
	candidates := map[string]string{}
	for name, s := range inst.config.Services {
		if s.Build != nil && s.Image != "" {
			candidates[name] = s.Image
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	t := inst.target()
	other, err := e.compose.ConfigFor(ctx, t)
	if err != nil {
		return nil, err
	}
	t.Name += "-theo-probe"
	probe, err := e.compose.ConfigFor(ctx, t)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name := range candidates {
		if other.Services[name].Image == probe.Services[name].Image {
			out[name] = other.Services[name].Image
		}
	}
	return out, nil
}
