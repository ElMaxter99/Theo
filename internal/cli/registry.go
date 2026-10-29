package cli

import (
	"io"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// A command registered with registerCommand runs with an engine and the
// arguments after its name. Commands added after the first ones live in
// their own file and register themselves in init, so adding one never
// touches Run or the help text of another.
type commandFunc func(e *engine.Engine, args []string, stdout, stderr io.Writer) error

var commands = map[string]commandFunc{}

// registerCommand makes `theo <name>` run fn.
func registerCommand(name string, fn commandFunc) {
	if _, dup := commands[name]; dup {
		panic("cli: command registered twice: " + name)
	}
	commands[name] = fn
}

type helpSection struct {
	order int
	text  string
}

var helpSections []helpSection

// registerHelp adds a block of text to the help, after the first commands.
// Blocks are ordered by order, then by text; each should end with a newline
// and start with a blank line.
func registerHelp(order int, text string) {
	helpSections = append(helpSections, helpSection{order, text})
}

// helpText returns the full help.
func helpText() string {
	sections := append([]helpSection(nil), helpSections...)
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].order != sections[j].order {
			return sections[i].order < sections[j].order
		}
		return sections[i].text < sections[j].text
	})
	var more strings.Builder
	for _, s := range sections {
		more.WriteString(s.text)
	}
	return strings.Replace(usageBase, "%MORE%", more.String(), 1)
}
