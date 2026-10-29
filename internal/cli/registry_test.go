package cli

import (
	"strings"
	"testing"
)

func TestRegisteredCommandsAndHelp(t *testing.T) {
	registerCommand("zz-test", nil)
	defer delete(commands, "zz-test")
	registerHelp(999, "\n  zz-test   A test command\n")
	defer func() { helpSections = helpSections[:len(helpSections)-1] }()

	help := helpText()
	if !strings.Contains(help, "zz-test   A test command") || strings.Contains(help, "%MORE%") {
		t.Errorf("help:\n%s", help)
	}
	if strings.Index(help, "zz-test") > strings.Index(help, "  version ") {
		t.Errorf("registered help must come before the footer commands")
	}
	defer func() {
		if recover() == nil {
			t.Errorf("registering a command twice must panic")
		}
	}()
	registerCommand("zz-test", nil)
}
