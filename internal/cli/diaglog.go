package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerCommand("log", runLog)
	registerHelp(41, `
  log [-n <lines>] [--path]                    Show the end of Theo's diagnostic log: what hooks and
                                               the MCP server could not do, with the time. --path
                                               prints where the file is. Attach it to a bug report
`)
	registerCompletion(cmdSpec{name: "log", flags: []flagSpec{{names: []string{"--path"}}, {[]string{"-n"}, "text"}}})
}

func runLog(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	lines := fs.Int("n", 50, "how many lines to show")
	pathOnly := fs.Bool("path", false, "print the path of the log")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 || *lines < 1 {
		return usageErrorf("log takes no arguments and -n must be at least 1")
	}
	path := e.DiagPath()
	if path == "" {
		return fmt.Errorf("Theo has no data directory, so no log")
	}
	if *pathOnly {
		fmt.Fprintln(stdout, path)
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) || len(strings.TrimSpace(string(data))) == 0 {
		fmt.Fprintf(stdout, "Nothing logged yet (%s).\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > *lines {
		all = all[len(all)-*lines:]
	}
	fmt.Fprintln(stdout, strings.Join(all, "\n"))
	return nil
}
