// Command theo manages development projects and isolated environments
// based on Git and Docker Compose.
package main

import (
	"os"

	"github.com/ElMaxter99/Theo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
