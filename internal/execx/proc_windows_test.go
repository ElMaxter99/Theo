//go:build windows

package execx

import (
	"os/exec"
	"testing"
)

func TestPrepareHidesTheConsoleOnlyWhenAsked(t *testing.T) {
	defer SetHideConsole(false)

	cmd := exec.Command("cmd")
	prepare(cmd)
	if cmd.SysProcAttr != nil {
		t.Errorf("the command line must keep its children on its console: %+v", cmd.SysProcAttr)
	}

	SetHideConsole(true)
	cmd = exec.Command("cmd")
	prepare(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf("a GUI program must start its children without a console window: %+v", cmd.SysProcAttr)
	}
}
