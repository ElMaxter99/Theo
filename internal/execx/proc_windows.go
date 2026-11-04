//go:build windows

package execx

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the child gets no console window.
const createNoWindow = 0x08000000

// killTree kills cmd and every process it started when its context ends.
// `docker compose ...` runs the Compose plugin as a child of docker.exe;
// killing docker.exe alone left docker-compose.exe running, for example a
// `logs --follow` after the browser closed the stream.
func killTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// streamKillTree is killTree: on Windows a streamed command is killed with
// its children too.
func streamKillTree(cmd *exec.Cmd) { killTree(cmd) }

// prepare hides the console window of the child when SetHideConsole(true) was called.
func prepare(cmd *exec.Cmd) {
	if hideConsole.Load() {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	}
}
