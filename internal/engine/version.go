package engine

import (
	"runtime/debug"
	"strings"
)

// VersionLine describes this build: "theo <version>", followed by the commit
// and date Go recorded in the binary when they are known (builds from a
// checkout have them; `go install module@version` does not). The line always
// starts with "theo <version>".
func VersionLine() string {
	line := "theo " + Version
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return line
	}
	var commit, date, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "+dirty"
			}
		}
	}
	var extra []string
	if commit != "" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		extra = append(extra, "commit "+commit+modified)
	}
	if date != "" {
		extra = append(extra, "built "+date)
	}
	if len(extra) > 0 {
		line += " (" + strings.Join(extra, ", ") + ")"
	}
	return line
}
