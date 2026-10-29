package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// serviceControl is implemented by Compose adapters that can stop, start or
// restart some services only (the real one does). It is optional so the
// Compose interface, shared with other work, stays as it is.
type serviceControl interface {
	Control(ctx context.Context, t compose.Target, verb string, services []string, stdout, stderr io.Writer) error
}

// serviceExec is implemented by Compose adapters that can run a command in a
// container.
type serviceExec interface {
	Exec(ctx context.Context, t compose.Target, opts compose.ExecOptions, stdin io.Reader, stdout, stderr io.Writer) error
}

// ServiceNames returns the services of the instance's Compose configuration,
// including the ones behind a profile.
func (inst *Instance) ServiceNames() []string {
	if inst.config == nil {
		return nil
	}
	return compose.SortedKeys(inst.config.Services)
}

// CheckServices fails, naming the valid ones, when a service does not exist
// in the instance's Compose configuration.
func (inst *Instance) CheckServices(names []string) error {
	if inst.config == nil {
		return nil
	}
	for _, n := range names {
		if _, ok := inst.config.Services[n]; !ok {
			return fmt.Errorf("the project has no service %q (services: %s)", n, strings.Join(inst.ServiceNames(), ", "))
		}
	}
	return nil
}

// Stop stops the containers of some services of an instance, or of all of
// them when services is empty. The containers are kept, with their volumes;
// the other services keep running. `Start` brings them back.
func (e *Engine) Stop(ctx context.Context, inst *Instance, services []string, stdout, stderr io.Writer) error {
	return e.control(ctx, inst, "stop", services, stdout, stderr)
}

// Start starts containers that exist and are stopped, for some services of
// an instance or for all of them when services is empty. It does not create
// containers: that is `Up`.
func (e *Engine) Start(ctx context.Context, inst *Instance, services []string, stdout, stderr io.Writer) error {
	return e.control(ctx, inst, "start", services, stdout, stderr)
}

// RestartServices restarts some services of an instance, or all of its
// containers when services is empty.
func (e *Engine) RestartServices(ctx context.Context, inst *Instance, services []string, stdout, stderr io.Writer) error {
	if len(services) == 0 {
		return e.Restart(ctx, inst, stdout, stderr)
	}
	return e.control(ctx, inst, "restart", services, stdout, stderr)
}

func (e *Engine) control(ctx context.Context, inst *Instance, verb string, services []string, stdout, stderr io.Writer) error {
	if err := inst.CheckServices(services); err != nil {
		return err
	}
	c, ok := e.compose.(serviceControl)
	if !ok {
		return errors.New("this Compose adapter cannot " + verb + " individual services")
	}
	err := c.Control(ctx, inst.target(), verb, services, stdout, stderr)
	e.record(inst, verb, err)
	return err
}

// Recreate recreates the containers of an instance, or only those of some
// services (without touching their dependencies), from the current image and
// configuration. Named volumes are not touched, so their data survives; use
// it after ApplyInstance, like Up, so the instance's settings apply.
func (e *Engine) Recreate(ctx context.Context, inst *Instance, opts compose.UpOptions, stdout, stderr io.Writer) error {
	if err := inst.CheckServices(opts.Services); err != nil {
		return err
	}
	opts.ForceRecreate = true
	opts.NoDeps = len(opts.Services) > 0
	err := e.compose.Up(ctx, inst.target(), opts, stdout, stderr)
	e.record(inst, "recreate", err)
	return err
}

// Exec runs a command in a running container of a service of the instance,
// connected to stdin, stdout and stderr. A command that exits with a non-zero
// status comes back as an *execx.ExitError.
func (e *Engine) Exec(ctx context.Context, inst *Instance, opts compose.ExecOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := inst.CheckServices([]string{opts.Service}); err != nil {
		return err
	}
	x, ok := e.compose.(serviceExec)
	if !ok {
		return errors.New("this Compose adapter cannot run commands in containers")
	}
	return x.Exec(ctx, inst.target(), opts, stdin, stdout, stderr)
}
