package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Pipe executes name with args, feeding it stdin (if not nil) and sending
// its standard output to stdout (if not nil). When the tool fails, the error
// includes its standard error output. It is meant for tools that read or
// write data streams, such as `docker run ... tar`.
//
// Under --explain the command is listed and not executed: it is used to move
// data in and out of volumes.
func Pipe(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	if explaining() {
		note("", name, args, true)
		return nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}
