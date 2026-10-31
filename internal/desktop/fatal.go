package desktop

import (
	"fmt"
	"io"
)

// ReportFatal tells the user why the app could not start: on stderr, and on
// Windows also in a dialog because that build has no console.
func ReportFatal(w io.Writer, err error) {
	msg := fmt.Sprintf("theo-desktop: %v", err)
	fmt.Fprintln(w, msg)
	showFatal(msg)
}
