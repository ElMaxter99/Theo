package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestAsciiWriterReplacesSymbols(t *testing.T) {
	var b bytes.Buffer
	w := asciiWriter{&b}
	n, err := w.Write([]byte("→ a · b … «x»"))
	if err != nil || n != len("→ a · b … «x»") {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got, want := b.String(), `-> a - b ... "x"`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunOutputIsASCIIWhenAsked(t *testing.T) {
	t.Setenv("THEO_HOME", t.TempDir())
	t.Setenv("THEO_ASCII", "1")
	var out, errb bytes.Buffer
	Run([]string{"nonsense-command"}, &out, &errb)
	for _, r := range out.String() + errb.String() {
		if r > 127 {
			t.Fatalf("non-ASCII %q in %q", r, out.String()+errb.String())
		}
	}
	if !strings.Contains(errb.String(), "->") {
		t.Fatalf("expected the ASCII arrow, got %q", errb.String())
	}
	// Not asked: the arrow stays.
	t.Setenv("THEO_ASCII", "0")
	out.Reset()
	errb.Reset()
	Run([]string{"nonsense-command"}, &out, &errb)
	if !strings.Contains(errb.String(), "→") {
		t.Fatalf("expected the arrow, got %q", errb.String())
	}
}

func TestRunJSONKeepsItsText(t *testing.T) {
	t.Setenv("THEO_HOME", t.TempDir())
	t.Setenv("THEO_ASCII", "1")
	var out, errb bytes.Buffer
	Run([]string{"version", "--json"}, &out, &errb)
	if !strings.Contains(out.String(), "version") {
		t.Fatalf("got %q", out.String())
	}
}
