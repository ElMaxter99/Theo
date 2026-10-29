package compose

import (
	"context"
	"io"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Control runs `docker compose stop`, `start` or `restart` on some services
// of the project, or on all of its containers when services is empty. The
// other services keep running. Profiles are all enabled, so that services
// behind a profile can be named.
func (Compose) Control(ctx context.Context, t Target, verb string, services []string, stdout, stderr io.Writer) error {
	sub := append([]string{verb}, services...)
	return execx.StreamChange(ctx, t.Dir, stdout, stderr, "docker", t.args([]string{"*"}, sub...)...)
}

// ExecOptions are the options of Exec.
type ExecOptions struct {
	Service string
	Command []string
	// NoTTY does not allocate a pseudo-terminal (docker compose exec -T). Set
	// it unless standard input is a terminal.
	NoTTY bool
}

// Exec runs a command in a running container of the service, connected to
// stdin, stdout and stderr. The exit status of the command comes back as an
// *execx.ExitError.
func (Compose) Exec(ctx context.Context, t Target, opts ExecOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	sub := []string{"exec"}
	if opts.NoTTY {
		sub = append(sub, "-T")
	}
	sub = append(sub, opts.Service)
	sub = append(sub, opts.Command...)
	return execx.StreamInteractive(ctx, t.Dir, stdin, stdout, stderr, "docker", t.args([]string{"*"}, sub...)...)
}
