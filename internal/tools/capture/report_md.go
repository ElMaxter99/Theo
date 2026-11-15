package capture

import (
	"fmt"
	"sort"
	"strings"
)

func pct(lang string, v float64) string {
	s := fmt.Sprintf("%.1f", v)
	if v > 0 && v < 0.1 {
		s = fmt.Sprintf("%.2f", v) // a changed word on a big screen is still not "0.0"
	}
	if lang == LangES {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// CompositionFile is the relative path of the before/after image of a state.
func CompositionFile(screen, state, viewport string) string {
	return "composiciones/" + safeName(screen) + "--" + safeName(state) + "--" + safeName(viewport) + ".png"
}

func flowLine(lang string, f FlowResult) []string {
	var lines []string
	switch f.Class {
	case ClassNew:
		return []string{Tr(lang, "flow.new", "`"+f.ID+"`")}
	case ClassRemoved:
		return []string{Tr(lang, "flow.removed", "`"+f.ID+"`")}
	}
	if f.Entry {
		a, b := "-", "-"
		if len(f.A) > 0 {
			a = "`" + f.A[0] + "`"
		}
		if len(f.B) > 0 {
			b = "`" + f.B[0] + "`"
		}
		return []string{"- **" + Tr(lang, "flow.entry") + "**: " + Tr(lang, "flow.entry.line", a, b)}
	}
	lines = append(lines, "- **"+f.ID+"**: `"+strings.Join(f.A, " → ")+"` → `"+strings.Join(f.B, " → ")+"`")
	for _, op := range f.Ops {
		var t string
		switch {
		case op.Kind == "remove":
			t = Tr(lang, "flow.remove", "`"+op.Screen+"`")
		case op.AtEnd:
			t = Tr(lang, "flow.add.end", "`"+op.Screen+"`")
		case op.After == "":
			t = Tr(lang, "flow.add.start", "`"+op.Screen+"`")
		default:
			t = Tr(lang, "flow.add.after", "`"+op.Screen+"`", "`"+op.After+"`")
		}
		lines = append(lines, "  - "+t)
	}
	return lines
}

// BuildSummary writes resumen.md: totals, flow changes, the screens that
// changed with their pixel percentage, the new, removed and moved ones, what
// could not be captured and what was unstable. A section with nothing in it
// is left out. It is meant to be pasted into a pull request or an issue.
func BuildSummary(c *Comparison, lang string) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	w("# %s", Tr(lang, "title", c.A.Label, c.B.Label))
	w("%s", Tr(lang, "meta", c.A.Label, shortCommit(c.A.Commit), c.B.Label, shortCommit(c.B.Commit), c.B.Date[:min(10, len(c.B.Date))], c.B.Mode, c.B.Chromium))
	n := c.Counts
	w("\n## %s\n%s", Tr(lang, "totals"), Tr(lang, "totals.line", n.Changed, n.Same, n.New, n.Removed, n.Moved, n.Unstable, n.NotCaptured))

	var flows []string
	for _, f := range c.Flows {
		if f.Class != "same" {
			flows = append(flows, flowLine(lang, f)...)
		}
	}
	if len(flows) > 0 {
		w("\n## %s", Tr(lang, "flows"))
		for _, l := range flows {
			w("%s", l)
		}
	}

	var rows []StateResult
	for _, s := range c.States {
		if s.Class == ClassChanged || s.Class == ClassMinor {
			rows = append(rows, s)
		}
	}
	if len(rows) > 0 {
		w("\n## %s", Tr(lang, "changed"))
		w("| %s | %s | %s | %s | %s |\n|---|---|---|---|---|", Tr(lang, "col.screen"), Tr(lang, "col.state"), Tr(lang, "col.viewport"), Tr(lang, "col.pixels"), Tr(lang, "col.image"))
		for _, s := range rows {
			w("| %s | %s | %s | %s %% | [%s](%s) |", s.Screen, s.State, s.Viewport, pct(lang, s.Percent), Tr(lang, "view"), CompositionFile(s.Screen, s.State, s.Viewport))
		}
	}
	list := func(class, key string) {
		var ids []string
		for _, s := range c.Screens {
			if s.Class == class {
				ids = append(ids, s.ID)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			w("\n## %s\n%s", Tr(lang, key), strings.Join(ids, " · "))
		}
	}
	list(ClassNew, "new")
	list(ClassRemoved, "removed")

	var moved []string
	for _, s := range c.Screens {
		if !s.Moved {
			continue
		}
		var p Pair
		for _, q := range c.Pairs {
			if q.BID == s.ID {
				p = q
			}
		}
		line := "- " + Tr(lang, "moved.line", firstNonEmpty(s.RouteA, s.FromID), firstNonEmpty(s.RouteB, s.ID), Tr(lang, "by."+p.Method))
		if p.Method == ByAI {
			line += " · " + Tr(lang, "ai.mark") + fmt.Sprintf(" (%.0f %%)", p.Confidence*100)
		}
		moved = append(moved, line)
	}
	if len(moved) > 0 {
		w("\n## %s", Tr(lang, "moved"))
		for _, l := range moved {
			w("%s", l)
		}
	}
	if len(c.NotCaptured) > 0 {
		w("\n## %s (%d)", Tr(lang, "notcaptured"), len(c.NotCaptured))
		for _, nc := range c.NotCaptured {
			switch nc.Kind {
			case "no_screen":
				w("- %s", Tr(lang, "nc.no_screen", nc.Item))
			case "no_mock":
				w("- %s", Tr(lang, "nc.no_mock", nc.Item, nc.Reason))
			default:
				w("- %s", Tr(lang, "nc.failed", nc.Item, nc.Reason))
			}
		}
	}
	var unstable []string
	for _, s := range c.States {
		if s.Class == ClassUnstable {
			unstable = append(unstable, fmt.Sprintf("- %s#%s %s: %s", s.Screen, s.State, s.Viewport, s.Reason))
		}
	}
	if len(unstable) > 0 {
		w("\n## %s (%d)", Tr(lang, "unstable"), len(unstable))
		for _, l := range unstable {
			w("%s", l)
		}
	}
	return b.String()
}

func shortCommit(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	if s == "" {
		return "-"
	}
	return s
}
