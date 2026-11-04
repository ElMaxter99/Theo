package cli

import (
	"os"
	"testing"
)

// The tests look for the symbols Theo prints (→, ·); on Windows the output
// is ASCII by default, so they ask for the symbols. The ASCII output has
// its own tests (asciiout_test.go).
func TestMain(m *testing.M) {
	os.Setenv("THEO_ASCII", "0")
	os.Exit(m.Run())
}
