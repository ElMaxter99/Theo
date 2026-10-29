package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerCommand("doctor", func(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
		return runDoctor(e, args, stdout)
	})
	registerHelp(40, `
  doctor [--json]                              Check that git, docker, the Docker daemon, the
                                               docker compose plugin (2.24.4 or later) and Theo's
                                               data directory work, and say how to fix what does
                                               not. Exit code 1 if something needed is missing
`)
}

func runDoctor(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("doctor takes no arguments")
	}

	report := e.Doctor(context.Background())
	if asJSON {
		if err := writeJSON(stdout, report); err != nil {
			return err
		}
	} else {
		printDoctor(stdout, report)
	}
	if !report.OK {
		failed := 0
		for _, c := range report.Checks {
			if c.Status == engine.StatusFail {
				failed++
			}
		}
		return fmt.Errorf("%d of %d checks failed", failed, len(report.Checks))
	}
	return nil
}

func printDoctor(w io.Writer, r engine.DoctorReport) {
	labels := map[string]string{engine.StatusOK: "ok", engine.StatusWarn: "warning", engine.StatusFail: "FAIL", engine.StatusInfo: "note"}
	for _, c := range r.Checks {
		fmt.Fprintf(w, "[%-7s] %s: %s\n", labels[c.Status], c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(w, "          fix: %s\n", c.Fix)
		}
	}
	if r.OK {
		fmt.Fprintln(w, "\nEverything Theo needs works.")
	} else {
		fmt.Fprintln(w, "\nSome checks failed. Fix them and run `theo doctor` again.")
	}
}
