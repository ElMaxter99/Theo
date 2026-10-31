//go:build !desktop

// Command theo-desktop is the Theo desktop app. It needs the native web view,
// so it only builds with the "desktop" tag:
//
//	go build -tags desktop ./cmd/theo-desktop
//
// Without the tag this file keeps `go vet ./...` and `go build ./...` working
// on machines that have no web view headers.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "theo-desktop was built without the \"desktop\" tag. Build it with: go build -tags desktop ./cmd/theo-desktop (see docs/cli/desktop.md)")
	os.Exit(2)
}
