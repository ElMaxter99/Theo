//go:build windows

package execx

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the child gets no console window.
const createNoWindow = 0x08000000

// killTree is a no-op on Windows: the command is killed with its context.
func killTree(*exec.Cmd) {}

// prepare hides the console window of the child when SetHideConsole(true) was called.
func prepare(cmd *exec.Cmd) {
	if hideConsole.Load() {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	}
}
