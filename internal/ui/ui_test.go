package ui

import (
	"io/fs"
	"testing"
)

func TestAssetsHaveAnIndex(t *testing.T) {
	if _, err := fs.Stat(Assets(), "index.html"); err != nil {
		t.Fatalf("the embedded interface has no index.html: %v", err)
	}
}
