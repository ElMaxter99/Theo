package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenExternal opens raw in the system browser if, and only if, Classify says
// it is another origin's http(s) address. No shell is involved: the address
// is a single argument of the opener.
func OpenExternal(raw, appOrigin string) error {
	switch Classify(raw, appOrigin) {
	case External:
		return systemOpen(raw)
	case Stay:
		return fmt.Errorf("refusing to open the Theo address in the browser")
	default:
		return fmt.Errorf("refusing to open an address that is not http or https")
	}
}

func systemOpen(raw string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
