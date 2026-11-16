// Command theo manages development projects and isolated environments
// based on Git and Docker Compose.
package main

import (
	"os"

	"github.com/ElMaxter99/Theo/internal/cli"
	"github.com/ElMaxter99/Theo/internal/update"
)

func main() {
	update.Cleanup(update.CurrentTarget()) // what an earlier update could not delete
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
