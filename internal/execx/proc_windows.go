//go:build windows

package execx

import "os/exec"

// killTree is a no-op on Windows: the command is killed with its context.
func killTree(*exec.Cmd) {}
