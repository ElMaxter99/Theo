package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// Error is the body of every non-2xx response: {"error": {...}}. Codes are
// stable and snake_case; messages are the engine's own, in English, never
// translated or summarised.
type Error struct {
	Status    int          `json:"-"`
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Hint      string       `json:"hint,omitempty"`
	Level     int          `json:"level,omitempty"`
	Resource  *ResourceRef `json:"resource,omitempty"`
	Confirm   *Confirm     `json:"confirm,omitempty"`
	Affected  []Affected   `json:"affected,omitempty"`
	Preserved []string     `json:"preserved,omitempty"`
	Original  string       `json:"original,omitempty"` // output of git or docker, trimmed to 8 KiB
	RequestID string       `json:"request_id,omitempty"`
	// Operation is the operation that is already running, for `busy`.
	Operation *OperationRef `json:"operation,omitempty"`
}

// ResourceRef says which resource an error is about.
type ResourceRef struct {
	Kind     string `json:"kind"`
	Project  string `json:"project,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

// Confirm describes the text a destructive request must carry.
type Confirm struct {
	Expected string `json:"expected"`
	Prompt   string `json:"prompt"`
}

// Affected is one thing a destructive action would change.
type Affected struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// OperationRef points to an operation.
type OperationRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func badRequest(msg string) *Error { return errf(http.StatusBadRequest, "invalid_request", msg) }

func projectNotFound(name string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "project_not_found", Message: "project " + quote(name) + " is not registered",
		Hint: "see GET /api/v1/projects", Resource: &ResourceRef{Kind: "project", Project: name}}
}

func worktreeNotFound(project, id string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "worktree_not_found", Message: "project " + quote(project) + " has no worktree " + quote(id),
		Hint: "see GET /api/v1/projects/" + project + "/worktrees", Resource: &ResourceRef{Kind: "worktree", Project: project, Worktree: id}}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

const maxOriginal = 8 << 10

func trim(s string) string {
	if len(s) > maxOriginal {
		return s[:maxOriginal] + "\n[truncated]"
	}
	return s
}

// engineErrorTypes lists every error type the engine defines. A test fails
// when the engine gains one that is not listed here, so that the mapping in
// fromEngine is never forgotten.
var engineErrorTypes = map[string]bool{
	"ChangesError":     true,
	"InstanceError":    true,
	"PurgeError":       true,
	"ConfirmError":     true,
	"SharedInUseError": true,
	"ContainersError":  true,
}

// fromEngine translates an error of the engine, Git or Docker into an API
// error. It is the only place where that mapping lives.
func fromEngine(err error, res *ResourceRef) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	preservedWorktree := []string{"the branch", "the other worktrees", "all volumes"}

	var changes *engine.ChangesError
	var instErr *engine.InstanceError
	var purge *engine.PurgeError
	var conf *engine.ConfirmError
	var shared *engine.SharedInUseError
	var cont *engine.ContainersError
	switch {
	case errors.As(err, &changes):
		out := &Error{Status: http.StatusConflict, Code: "confirmation_required", Level: 3, Message: err.Error(), Resource: res,
			Confirm:   &Confirm{Expected: changes.Name, Prompt: "Type the directory name to discard these changes"},
			Preserved: preservedWorktree}
		for _, c := range changes.Changes {
			status, name := splitPorcelain(c)
			out.Affected = append(out.Affected, Affected{Kind: "file", Name: name, Detail: status})
		}
		return out
	case errors.As(err, &instErr):
		out := &Error{Status: http.StatusConflict, Code: "instance_has_containers", Message: err.Error(),
			Hint: "stop the instance (down) before removing the worktree", Resource: res}
		for _, s := range instErr.Stacks {
			out.Affected = append(out.Affected, Affected{Kind: "compose_project", Name: s})
		}
		return out
	case errors.As(err, &purge):
		out := &Error{Status: http.StatusConflict, Code: purge.Code(), Message: err.Error(), Resource: res}
		for _, v := range purge.Volumes {
			out.Affected = append(out.Affected, Affected{Kind: "volume", Name: v.Name, Detail: v.Status})
		}
		switch purge.Code() {
		case engine.PurgeNeedsConfirm:
			out.Level = 3
			out.Confirm = &Confirm{Expected: purge.Instance, Prompt: "Type the instance name to delete its data"}
			out.Preserved = []string{"external volumes", "other instances", "the branch and the worktree"}
		case engine.PurgeHasContainers:
			out.Hint = "stop the instance (down) first; its volumes are kept"
		}
		return out
	case errors.As(err, &conf):
		return &Error{Status: http.StatusConflict, Code: "confirmation_required", Level: 3, Message: err.Error(), Resource: res,
			Confirm: &Confirm{Expected: conf.Instance, Prompt: "Type the instance name to confirm"}}
	case errors.As(err, &shared):
		out := &Error{Status: http.StatusConflict, Code: shared.Code(), Message: err.Error(), Resource: res}
		for _, c := range shared.Consumers {
			out.Affected = append(out.Affected, Affected{Kind: "instance", Name: c.Workspace})
		}
		if shared.Confirm != "" {
			out.Level = 2
			out.Confirm = &Confirm{Expected: shared.Confirm, Prompt: "Type the text shown to confirm"}
		}
		return out
	case errors.As(err, &cont):
		out := &Error{Status: http.StatusConflict, Code: "project_has_containers", Message: err.Error(), Resource: res,
			Hint: "stop the instances first"}
		for _, s := range cont.Stacks {
			out.Affected = append(out.Affected, Affected{Kind: "compose_project", Name: s.Name, Detail: s.Status})
		}
		return out
	case errors.Is(err, context.Canceled):
		return &Error{Status: http.StatusConflict, Code: "cancelled", Message: "the operation was cancelled", Resource: res}
	case errors.Is(err, execx.ErrNotFound):
		return &Error{Status: http.StatusPreconditionFailed, Code: toolUnavailable(err), Message: err.Error(), Resource: res,
			Hint: "run `theo doctor` for how to install it"}
	case errors.Is(err, engine.ErrNotFound):
		msg := err.Error()
		code, kind := "project_not_found", "project"
		if strings.Contains(msg, "worktree \"") {
			code, kind = "worktree_not_found", "worktree"
		}
		out := &Error{Status: http.StatusNotFound, Code: code, Message: msg, Resource: res}
		if out.Resource == nil {
			out.Resource = &ResourceRef{Kind: kind}
		}
		return out
	}

	msg := err.Error()
	low := strings.ToLower(msg)
	out := &Error{Status: http.StatusUnprocessableEntity, Code: "engine_error", Message: firstParagraph(msg), Original: trim(msg), Resource: res}
	if out.Message == out.Original {
		out.Original = ""
	}
	switch {
	case strings.Contains(low, "cannot connect to the docker daemon"), strings.Contains(low, "is the docker daemon running"),
		strings.Contains(low, "error during connect"), strings.Contains(low, "docker daemon"):
		out.Status, out.Code, out.Hint = http.StatusPreconditionFailed, "docker_unavailable", "start Docker and try again; `theo doctor` checks the installation"
	case strings.Contains(low, "is too old"):
		if strings.Contains(low, "compose") {
			out.Status, out.Code, out.Hint = http.StatusPreconditionFailed, "compose_too_old", "run `theo doctor` for how to upgrade"
		} else {
			out.Status, out.Code, out.Hint = http.StatusPreconditionFailed, "git_too_old", "run `theo doctor` for how to upgrade"
		}
	case strings.Contains(low, "is already checked out"), strings.Contains(low, "already exists and is checked out"):
		out.Status, out.Code = http.StatusConflict, "branch_checked_out"
	case strings.Contains(low, "port is already allocated"), strings.Contains(low, "address already in use"), strings.Contains(low, "ports are not available"):
		out.Status, out.Code, out.Hint = http.StatusConflict, "port_conflict", "start with auto_ports to use free ports"
	case strings.Contains(low, "already registered"), strings.Contains(low, "already exists"):
		out.Status, out.Code = http.StatusConflict, "already_exists"
	case strings.Contains(low, "docker compose config"), strings.Contains(low, "invalid compose"), strings.Contains(low, "validating "):
		out.Status, out.Code = http.StatusUnprocessableEntity, "compose_invalid"
	case strings.Contains(low, "invalid project name"), strings.Contains(low, "invalid variable name"):
		out.Status, out.Code = http.StatusBadRequest, "invalid_name"
	}
	return out
}

func toolUnavailable(err error) string {
	if strings.HasPrefix(err.Error(), "git") {
		return "git_unavailable"
	}
	return "docker_unavailable"
}

func firstParagraph(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// splitPorcelain splits a `git status --porcelain` line into its status and
// path.
func splitPorcelain(line string) (status, name string) {
	if len(line) > 3 && line[2] == ' ' {
		return strings.TrimSpace(line[:2]), line[3:]
	}
	return "", strings.TrimSpace(line)
}
