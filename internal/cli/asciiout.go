package cli

import (
	"io"
	"os"
	"runtime"
	"strings"
)

// The console of Windows PowerShell 5.1 reads what a program writes with the
// old OEM code page (850, 437...), not as UTF-8, so the arrow of "→ theo help"
// showed up as "ÔåÆ". Theo cannot change how the console decodes, so on Windows
// it writes plain ASCII instead of the few symbols it uses. THEO_ASCII=1
// does it on any system and THEO_ASCII=0 never does it.

var asciiReplacer = strings.NewReplacer(
	"→", "->", "·", "-", "…", "...", "—", "-", "–", "-",
	"▶", ">", "◐", "~", "▲", "!", "■", "#", "○", "o", "✓", "ok", "✗", "x",
	"«", `"`, "»", `"`, "¿", "?", "¡", "!",
)

// useASCII says whether the output of this process must be ASCII only.
func useASCII() bool {
	switch os.Getenv("THEO_ASCII") {
	case "1":
		return true
	case "0":
		return false
	}
	return runtime.GOOS == "windows"
}

// asciiWriter replaces the symbols of asciiReplacer in what is written. It
// keeps the file under it reachable, so that color detection still sees a
// terminal.
type asciiWriter struct{ w io.Writer }

func (a asciiWriter) Write(p []byte) (int, error) {
	// A symbol is never split across two writes by Theo: every Fprint writes
	// whole strings.
	if _, err := io.WriteString(a.w, asciiReplacer.Replace(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Unwrap gives the writer under, for code that needs the *os.File.
func (a asciiWriter) Unwrap() io.Writer { return a.w }

// asciiOnly wraps w when the output must be ASCII only.
func asciiOnly(w io.Writer) io.Writer {
	if w == nil || !useASCII() {
		return w
	}
	return asciiWriter{w}
}

// underlying follows Unwrap down to the writer that is not a wrapper.
func underlying(w io.Writer) io.Writer {
	for {
		u, ok := w.(interface{ Unwrap() io.Writer })
		if !ok {
			return w
		}
		w = u.Unwrap()
	}
}

func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}
