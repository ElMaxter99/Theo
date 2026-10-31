package api

import "strings"

// networkPath reports whether p, on the given OS, names something Windows
// reaches over the network or through the device namespace: a UNC path
// (\\server\share), \\?\UNC\..., \\.\pipe\... and the like. Merely listing such
// a path makes Windows connect to the named host and send the user's
// credentials to it, so the directory browser does not follow it. The WSL
// file systems (\\wsl$\..., \\wsl.localhost\...) are local and allowed, as
// are drive paths in the \\?\C:\ form.
func networkPath(p, goos string) bool {
	if goos == "windows" {
		p = strings.ReplaceAll(p, "/", `\`)
	}
	rest, ok := strings.CutPrefix(p, `\\`)
	if !ok {
		return false
	}
	if after, ok := strings.CutPrefix(rest, `?\`); ok { // \\?\C:\dir is local, \\?\UNC\host\share is not
		return !(len(after) >= 2 && after[1] == ':' && (len(after) == 2 || after[2] == '\\'))
	}
	host, _, _ := strings.Cut(rest, `\`)
	switch strings.ToLower(host) {
	case "wsl$", "wsl.localhost":
		return false
	}
	return true // includes \\.\ (devices) and \\host\share
}
