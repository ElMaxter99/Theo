// Package execx runs external tools (git, docker) directly, without a shell,
// and turns their failures into errors that carry the tool's own message.
//
// Every call says whether it only reads (Run, Stream) or changes something
// (RunChange, StreamChange, StreamInteractive). That distinction is what
// `theo --explain` builds on: see explain.go.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ErrNotFound is returned when the requested tool is not installed or not
// in PATH.
var ErrNotFound = errors.New("not found in PATH")

// ExitError is returned by StreamInteractive when the command ran and exited
// with a non-zero status. Code is that status.
type ExitError struct {
	Command string
	Code    int
}

func (e *ExitError) Error() string { return fmt.Sprintf("%s exited with status %d", e.Command, e.Code) }

// Run executes name with args in dir and returns its standard output. When
// the tool fails, the error includes its standard error output. Run is for
// commands that only read: under --explain it runs them (and lists them).
// Use RunChange for commands that change Git or Docker.
func Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if explaining() {
		mutating := looksMutating(name, args)
		note(dir, name, args, mutating)
		if mutating {
			return nil, nil
		}
	}
	return run(ctx, dir, name, args...)
}

// RunChange is Run for a command that changes something (creates or removes
// worktrees, volumes, networks...). Under --explain it is listed and not
// executed: it returns no output and no error.
func RunChange(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if explaining() {
		note(dir, name, args, true)
		return nil, nil
	}
	return run(ctx, dir, name, args...)
}

func run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}

	lim := limitsOf(ctx)
	key := breakerKey(name, dir, args)
	if lim.Breaker != nil {
		if err := lim.Breaker.peek(key); err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
	}
	release, err := acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	defer release()
	if lim.Breaker != nil { // it may have opened while this command waited for a slot
		if err := lim.Breaker.check(key); err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
	}
	runCtx := ctx
	if lim.Timeout > 0 { // counted from the start of the command, not from the wait for a slot
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, lim.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, path, args...)
	cmd.Dir = dir
	if lim.Timeout > 0 {
		killTree(cmd)
	}
	cmd.WaitDelay = 2 * time.Second // a grandchild holding the pipes cannot keep us waiting after the kill
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if lim.Timeout > 0 && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			if lim.Breaker != nil {
				lim.Breaker.trip(key)
			}
			return nil, fmt.Errorf("%s %s: %w after %s", name, strings.Join(args, " "), ErrTimeout, lim.Timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if lim.Breaker != nil {
			if runCtx.Err() == nil {
				lim.Breaker.worked(key) // it answered, even if with a failure
			} else {
				lim.Breaker.abandon(key) // the caller gave up: nothing learned
			}
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	if lim.Breaker != nil {
		lim.Breaker.worked(key)
	}
	return stdout.Bytes(), nil
}

// Stream executes name with args in dir, sending its output straight to
// stdout and stderr. It is meant for long commands that only read, such as
// `docker compose logs -f`. Use StreamChange for the ones that change
// something, such as `docker compose up`.
func Stream(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	if explaining() {
		mutating := looksMutating(name, args)
		note(dir, name, args, mutating)
		if mutating {
			return nil
		}
	}
	return stream(ctx, dir, nil, stdout, stderr, name, args...)
}

// StreamChange is Stream for a command that changes something. Under
// --explain it is listed and not executed.
func StreamChange(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	if explaining() {
		note(dir, name, args, true)
		return nil
	}
	return stream(ctx, dir, nil, stdout, stderr, name, args...)
}

// StreamInteractive is StreamChange with standard input: the command reads
// stdin and writes to stdout and stderr, as `docker compose exec` does. When
// the command runs and exits with a non-zero status, the error is an
// *ExitError carrying that status, so the caller can pass it on. Under
// --explain it is listed and not executed.
func StreamInteractive(ctx context.Context, dir string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	if explaining() {
		note(dir, name, args, true)
		return nil
	}
	err := stream(ctx, dir, stdin, stdout, stderr, name, args...)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() >= 0 {
		return &ExitError{Command: name + " " + strings.Join(args, " "), Code: ee.ExitCode()}
	}
	return err
}

func stream(ctx context.Context, dir string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
