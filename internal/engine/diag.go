package engine

import (
	"fmt"
	"github.com/ElMaxter99/Theo/internal/execx"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiagFile is Theo's diagnostic log, in the data directory.
const DiagFile = "theo.log"

const diagMax = 512 << 10

// DiagPath is where the diagnostic log is.
func (e *Engine) DiagPath() string {
	if e.dataDir == "" {
		return ""
	}
	return filepath.Join(e.dataDir, DiagFile)
}

// Diag appends a line to the diagnostic log. Hooks and the MCP server have
// no terminal, so this is where their failures can be found afterwards. It
// never fails: a log that cannot be written is not worth an error. The file
// is cut to its last half when it passes 512 KiB.
func (e *Engine) Diag(format string, a ...any) {
	path := e.DiagPath()
	if path == "" || execx.Explaining() {
		return
	}
	line := fmt.Sprintf("%s %s\n", e.now().UTC().Format(time.RFC3339), strings.ReplaceAll(fmt.Sprintf(format, a...), "\n", " | "))
	if info, err := os.Stat(path); err == nil && info.Size() > diagMax {
		if data, err := os.ReadFile(path); err == nil {
			keep := data[len(data)/2:]
			if i := strings.IndexByte(string(keep), '\n'); i >= 0 {
				keep = keep[i+1:]
			}
			_ = os.WriteFile(path, keep, 0o600)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
