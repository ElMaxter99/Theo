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

// openIn starts the tool for a target in the folder of an instance.
func openIn(e *engine.Engine, inst *engine.Instance, target string) (string, error) {
	label, err := opener.Open(e.Prefs(), target, inst.Workspace)
	if errors.Is(err, opener.ErrTarget) {
		return "", usageErrorf("--in must be one of %s", strings.Join(openTargets, ", "))
	}
	if err != nil && strings.HasPrefix(err.Error(), "the folder no longer exists") {
		return "", fmt.Errorf("the folder of %s no longer exists: %s", worktreeArg(inst), inst.Workspace)
	}
	return label, err
}
