//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

// buildCommand runs the command line with cmd.exe. The line is passed as it
// is (CmdLine, with /S so only the outer quotes are removed): Go would escape
// the quotes of a path such as "C:\Program Files\node\node.exe" with
// backslashes, which cmd.exe does not understand.
func buildCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       `cmd.exe /S /C "` + command + `"`,
	}
	return cmd
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
