//go:build windows

package update

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP: the new app outlives this one.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
}
