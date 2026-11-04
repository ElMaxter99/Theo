package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/prefs"
)

func init() {
	registerCommand("config", runConfig)
	registerHelp(42, `
  config list | get <key> | set <key> <value> | unset <key> [--json]
                                               Your own settings: open.editor, open.terminal, open.files
                                               (what theo open --in starts) and update-check (on or off:
                                               look once a day for a new version; off by default)
`)
	registerCompletion(cmdSpec{name: "config", subs: []cmdSpec{{name: "list"}, {name: "get"}, {name: "set"}, {name: "unset"}}})
}

func runConfig(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("config needs a subcommand: list, get, set or unset")
	}
	sub := args[0]
	fs := flag.NewFlagSet("config "+sub, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	store := e.Prefs()
	need := map[string]int{"list": 0, "get": 1, "set": 2, "unset": 1}
	n, ok := need[sub]
	if !ok {
		return usageErrorf("unknown config subcommand %q", sub)
	}
	if len(pos) != n {
		return usageErrorf("config %s takes %d argument(s)", sub, n)
	}
	switch sub {
	case "list":
		all := store.All()
		if *asJSON {
			return writeJSON(stdout, all)
		}
		keys := make([]string, 0, len(prefs.Keys))
		for k := range prefs.Keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := all[k]
			if v == "" {
				v = "(default)"
			}
			fmt.Fprintf(stdout, "%-14s %s\n", k, v)
		}
	case "get":
		if _, ok := prefs.Keys[pos[0]]; !ok {
			return usageErrorf("unknown setting %q (%s)", pos[0], prefs.KeyList())
		}
		v := store.Get(pos[0])
		if *asJSON {
			return writeJSON(stdout, map[string]string{"key": pos[0], "value": v})
		}
		fmt.Fprintln(stdout, v)
	case "set":
		if err := store.Set(pos[0], pos[1]); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, map[string]string{"key": pos[0], "value": pos[1]})
		}
		fmt.Fprintf(stdout, "%s = %s\n", pos[0], pos[1])
		if pos[0] == "update-check" && pos[1] == "on" {
			fmt.Fprintln(stdout, "Theo will ask GitHub once a day whether a new version exists. Nothing else is sent. Without a network it does nothing.")
		}
	case "unset":
		if err := store.Unset(pos[0]); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, map[string]string{"key": pos[0]})
		}
		fmt.Fprintf(stdout, "%s is back to its default\n", pos[0])
	}
	return nil
}
