package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func runReconcile(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "forget Theo's own stale records (never containers, volumes or files of yours)")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("reconcile takes at most one project name")
	}
	project := ""
	if len(pos) == 1 {
		project = pos[0]
	}
	ctx := context.Background()
	report, err := e.Reconcile(ctx, project)
	if err != nil {
		return err
	}

	forgot := 0
	if *apply {
		if forgot, err = e.ApplyReconcile(report); err != nil {
			return err
		}
	}
	if *asJSON {
		return writeJSON(stdout, struct {
			*engine.Report
			Forgotten int `json:"forgotten"`
		}{report, forgot})
	}

	if report.DockerError != "" {
		fmt.Fprintf(stdout, "Docker could not be checked, so containers and volumes are missing from this report:\n  %s\n\n", firstLine(report.DockerError))
	}
	if len(report.Findings) == 0 {
		fmt.Fprintln(stdout, "Everything Theo recorded matches what exists.")
		return nil
	}
	for _, f := range report.Findings {
		label := f.Kind
		if f.Project != "" {
			label += " · " + f.Project
		}
		fmt.Fprintf(stdout, "%s: %s\n  %s\n", label, f.Subject, f.Detail)
		if f.Fix != "" && !(*apply && f.Forgettable) {
			fmt.Fprintf(stdout, "  -> %s\n", f.Fix)
		}
	}
	switch {
	case *apply && forgot > 0:
		fmt.Fprintf(stdout, "\nForgot %d of Theo's own record(s). Nothing else was touched.\n", forgot)
	case *apply:
		fmt.Fprintln(stdout, "\nNothing of Theo's own to forget; the rest needs you (see the lines marked ->).")
	default:
		if n := countForgettable(report); n > 0 {
			fmt.Fprintf(stdout, "\n%d record(s) of Theo's own can be forgotten with `theo reconcile%s --apply`. Nothing was changed.\n", n, projectArg(project))
		} else {
			fmt.Fprintln(stdout, "\nNothing was changed.")
		}
	}
	return nil
}

func countForgettable(r *engine.Report) int {
	n := 0
	for _, f := range r.Findings {
		if f.Forgettable {
			n++
		}
	}
	return n
}

func projectArg(project string) string {
	if project == "" {
		return ""
	}
	return " " + strings.TrimSpace(project)
}
