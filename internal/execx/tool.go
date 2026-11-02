package execx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnvDocker names the program Theo runs whenever it runs `docker`: an
// absolute path, or a command name looked up in PATH. It is for a
// docker-compatible CLI that is not first in PATH (a second Docker install, a
// wrapper script). Theo still passes it Docker's arguments and parses
// Docker's output formats, so it must be a real docker CLI or behave exactly
// like one; Podman's own `podman` command does not (see docs/cli/engines.md).
//
// Unset or empty, Theo runs `docker` from PATH. DOCKER_HOST, DOCKER_CONTEXT
// and every other variable are passed on unchanged in both cases: Theo never
// sets the environment of the commands it runs.
const EnvDocker = "THEO_DOCKER"

// DockerOverride returns the value of THEO_DOCKER, trimmed, or "".
func DockerOverride() string { return strings.TrimSpace(os.Getenv(EnvDocker)) }

// lookPath finds the program to run for name. For "docker" it honours
// THEO_DOCKER. A relative path with a directory in it is refused: commands
// run in other directories (cmd.Dir), where it would name another file.
func lookPath(name string) (string, error) {
	if name == "docker" {
		if alt := DockerOverride(); alt != "" {
			if strings.ContainsAny(alt, `/\`) && !filepath.IsAbs(alt) {
				return "", fmt.Errorf("%s=%s: must be an absolute path or a command name in PATH: %w", EnvDocker, alt, ErrNotFound)
			}
			path, err := exec.LookPath(alt)
			if err != nil {
				return "", fmt.Errorf("%s=%s: %w", EnvDocker, alt, ErrNotFound)
			}
			return path, nil
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	return path, nil
}
