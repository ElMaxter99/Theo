package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
	"github.com/ElMaxter99/Theo/internal/tools"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func init() {
	registerCommand("tool", runTool)
	registerHelp(55, `
  tool list [--json]                           Theo's tools (self-contained features with their own scope)
  tool capture detect [path] [--write] [--json]
                                               Work out how a project starts and propose the "run" section
                                               of .theo.json (Compose, a package.json script or a Makefile
                                               target). --write adds it to .theo.json; it never replaces a
                                               run section that is already there
`)
	registerCompletion(cmdSpec{name: "tool", subs: []cmdSpec{
		{name: "list"},
		{name: "capture", subs: []cmdSpec{{name: "detect", flags: []flagSpec{{names: []string{"--write"}}, {names: []string{"--json"}}}}}},
	}})
}

func runTool(_ *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("tool needs a subcommand: list or <tool> <command> (see `theo tool list`)")
	}
	if args[0] == "list" {
		fl := flag.NewFlagSet("tool list", flag.ContinueOnError)
		asJSON := fl.Bool("json", false, "print JSON")
		if _, err := parse(fl, args[1:]); err != nil {
			return err
		}
		list := tools.List()
		if *asJSON {
			return writeJSON(stdout, list)
		}
		for _, t := range list {
			fmt.Fprintf(stdout, "%-10s %s\n", t.ID, t.Title)
			fmt.Fprintf(stdout, "%-10s %s\n", "", t.Summary)
			if len(t.Commands) > 0 {
				fmt.Fprintf(stdout, "%-10s works today: %s\n", "", strings.Join(t.Commands, ", "))
			}
			if len(t.Planned) > 0 {
				fmt.Fprintf(stdout, "%-10s planned: %s\n", "", strings.Join(t.Planned, ", "))
			}
		}
		return nil
	}
	if _, ok := tools.Get(args[0]); !ok {
		return usageErrorf("unknown tool %q (see `theo tool list`)", args[0])
	}
	if args[0] == capture.ID {
		return runCapture(args[1:], stdout, stderr)
	}
	return usageErrorf("tool %q has no commands yet", args[0])
}

func runCapture(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "detect" {
		return usageErrorf("tool capture works today with: detect (the rest is planned; see `theo tool list`)")
	}
	fl := flag.NewFlagSet("tool capture detect", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	write := fl.Bool("write", false, "add the proposal to .theo.json")
	pos, err := parse(fl, args[1:])
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("tool capture detect takes one folder")
	}
	root := "."
	if len(pos) == 1 {
		root = pos[0]
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	d, err := capture.Detect(root)
	if err != nil {
		return err
	}
	wrote := false
	if *write {
		if d.Run == nil {
			return errors.New("nothing to write: no way to start this project was found")
		}
		if wrote, err = writeRunSection(root, d); err != nil {
			return err
		}
	}
	if *asJSON {
		return writeJSON(stdout, map[string]any{"detection": d, "written": wrote})
	}
	if d.Run == nil {
		fmt.Fprintln(stdout, "No way to start this project was found.")
	} else {
		fmt.Fprintf(stdout, "Proposed run section (%s confidence):\n", d.Confidence)
		b, _ := json.MarshalIndent(map[string]any{"run": d.Run}, "", "  ")
		fmt.Fprintln(stdout, string(b))
	}
	for _, e := range d.Evidence {
		fmt.Fprintf(stdout, "  because: %s\n", e)
	}
	for _, n := range d.Notes {
		fmt.Fprintf(stdout, "  note: %s\n", n)
	}
	switch {
	case wrote:
		fmt.Fprintf(stdout, "Written to %s. Its commands run code from the repository, so Theo asks before running them.\n", repoconfig.FileName)
	case d.Run != nil && !*write:
		fmt.Fprintln(stdout, "Nothing was written. Add --write to put it in .theo.json.")
	}
	return nil
}

// writeRunSection adds the proposal to .theo.json, keeping what the file
// already says. It returns false when the file already has a run section.
func writeRunSection(root string, d capture.Detection) (bool, error) {
	path := filepath.Join(root, repoconfig.FileName)
	doc := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &doc); err != nil {
			return false, fmt.Errorf("%s is not valid: %w", repoconfig.FileName, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	if _, ok := doc["run"]; ok {
		return false, nil
	}
	run, err := json.Marshal(d.Run)
	if err != nil {
		return false, err
	}
	doc["run"] = run
	if _, ok := doc["version"]; !ok {
		doc["version"] = json.RawMessage("1")
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o644)
}
