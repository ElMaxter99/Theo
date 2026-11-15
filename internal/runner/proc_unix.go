//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// buildCommand runs the command line with sh, in its own process group, so the
// whole tree can be signalled at once.
func buildCommand(command string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// terminate asks the group to stop and, if it has not after a few seconds,
// kills it.
func terminate(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	// Children that ignored SIGTERM, or outlived the leader, go too.
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}
