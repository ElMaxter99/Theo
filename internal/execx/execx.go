// Package execx runs external tools (git, docker) directly, without a shell,
// and turns their failures into errors that carry the tool's own message.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ErrNotFound is returned when the requested tool is not installed or not
// in PATH.
var ErrNotFound = errors.New("not found in PATH")

// Run executes name with args in dir and returns its standard output. When
// the tool fails, the error includes its standard error output.
func Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Stream executes name with args in dir, sending its output straight to
// stdout and stderr. It is meant for long or interactive commands such as
// `docker compose up` or `docker compose logs -f`.
func Stream(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
