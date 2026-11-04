package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/opener"
)

// openTargets are the values of `theo open --in`.
var openTargets = append(append([]string{}, opener.Targets...), "url")

// openIn starts the tool for a target in the folder of a worktree.
func openIn(e *engine.Engine, path, branch, target string) (string, error) {
	label, err := opener.Open(e.Prefs(), target, path)
	if errors.Is(err, opener.ErrTarget) {
		return "", usageErrorf("--in must be one of %s", strings.Join(openTargets, ", "))
	}
	if err != nil && strings.HasPrefix(err.Error(), "the folder no longer exists") {
		return "", fmt.Errorf("the folder of %s no longer exists: %s", firstNonEmpty(branch, path), path)
	}
	return label, err
}
