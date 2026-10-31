package api

import "regexp"

// The diagnostics report is meant to be pasted into a public issue. Paths in
// it carry the user name and the layout of the machine, so they are replaced.
//
// A path runs to the next space, comma, semicolon, quote or parenthesis, and
// does not end in a colon (the colon after it belongs to the sentence). A
// path starts at a word boundary, so URLs (https://...) are left alone.
const pathBody = `(?:[^\s,;"')]*[^\s,;"'):])?`

var (
	winDrivePath = regexp.MustCompile(`(^|[^A-Za-z0-9])[A-Za-z]:[\\/]` + pathBody)
	winUNCPath   = regexp.MustCompile(`\\\\` + pathBody)
	posixPath    = regexp.MustCompile(`(^|[\s"'(=,;]|://)/` + pathBody) // also unix:///var/run/docker.sock, file:///...
)

const redacted = "<path>"

// redactPaths replaces the absolute paths in s (POSIX, Windows drive and UNC).
func redactPaths(s string) string {
	s = winUNCPath.ReplaceAllString(s, redacted)
	s = winDrivePath.ReplaceAllString(s, "${1}"+redacted)
	return posixPath.ReplaceAllString(s, "${1}"+redacted)
}
