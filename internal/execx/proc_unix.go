//go:build !windows

package execx

import (
	"os/exec"
	"syscall"
)

// killTree makes cmd run in its own process group and, when its context ends,
// kills the whole group: a `docker` that has handed work to a plugin or a
// child must not leave that child running (and holding our pipes) behind.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
