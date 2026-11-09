package notify

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Send shows a desktop notification with the system's own tool: notify-send
// on Linux, osascript on macOS and PowerShell on Windows. It has no buttons;
// the person finds the worktree in Theo's window.
func Send(m Message) error {
	switch runtime.GOOS {
	case "linux":
		path, err := exec.LookPath("notify-send")
		if err != nil {
			return errors.New("notify-send not found: install libnotify to get desktop notifications")
		}
		return exec.Command(path, "--app-name=Theo", "--", m.Title, m.Body).Run()
	case "darwin":
		return exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s"`, appleScriptEscape(m.Body), appleScriptEscape(m.Title))).Run()
	case "windows":
		return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", windowsToast(m)).Run()
	}
	return fmt.Errorf("desktop notifications are not supported on %s", runtime.GOOS)
}

func appleScriptEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s)
}

// windowsToast is the PowerShell that shows a toast. The text goes in single
// quotes, doubled inside, and the XML is escaped.
func windowsToast(m Message) string {
	x := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", "\"", "&quot;")
	q := func(s string) string { return strings.ReplaceAll(x.Replace(s), "'", "''") }
	return `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; ` +
		`$x = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]::new(); ` +
		`$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>` + q(m.Title) + `</text><text>` + q(m.Body) + `</text></binding></visual></toast>'); ` +
		`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Theo').Show([Windows.UI.Notifications.ToastNotification]::new($x))`
}
