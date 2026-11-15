//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

func shellCommand(cmd string) (string, []string) { return "cmd", []string{"/C", cmd} }

func setupTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// terminate kills the process and everything it started.
func terminate(cmd *exec.Cmd, _ <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := kill.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
