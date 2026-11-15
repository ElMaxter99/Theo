// Package capture is the screen capture tool: it takes two deployments of an
// application through a list of screens and reports what changed to the eye.
// This package holds what exists so far: working out how a project starts.
package capture

import "github.com/ElMaxter99/Theo/internal/tools"

// ID of the tool.
const ID = "capture"

func init() {
	tools.Register(tools.Tool{
		ID:       ID,
		Title:    "Screen capture, before and after",
		Summary:  "Capture the screens of two deployments of an application and compare them.",
		Scope:    "capture",
		Commands: []string{"detect", "serve", "validate", "init", "setup", "run", "run-pair", "sets", "compare", "report"},
		Planned:  []string{"generate"},
	})
}
