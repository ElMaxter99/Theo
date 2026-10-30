package engine

import (
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

func TestInstanceState(t *testing.T) {
	run := func(svc string) compose.Container { return compose.Container{Service: svc, State: "running"} }
	tests := []struct {
		name       string
		containers []compose.Container
		expected   []string
		dockerOK   bool
		want       string
	}{
		{"docker down", nil, nil, false, InstUnknown},
		{"docker down wins over containers", []compose.Container{run("web")}, nil, false, InstUnknown},
		{"no containers", nil, []string{"web"}, true, InstAbsent},
		{"all running", []compose.Container{run("web"), run("db")}, []string{"db", "web"}, true, InstRunning},
		{"running, expected unknown", []compose.Container{run("web")}, nil, true, InstRunning},
		{"one expected service missing", []compose.Container{run("web")}, []string{"db", "web"}, true, InstPartial},
		{"one stopped cleanly beside a running one", []compose.Container{run("web"), {Service: "db", State: "exited"}}, []string{"db", "web"}, true, InstRunning},
		{"service stopped cleanly and never expected to run", []compose.Container{run("web"), {Service: "job", State: "exited"}}, []string{"web"}, true, InstRunning},
		{"profile service running is fine", []compose.Container{run("web"), run("debug")}, []string{"web"}, true, InstRunning},
		{"all stopped cleanly", []compose.Container{{Service: "web", State: "exited"}, {Service: "db", State: "exited"}}, []string{"db", "web"}, true, InstStopped},
		{"created only", []compose.Container{{Service: "web", State: "created"}}, []string{"web"}, true, InstStopped},
		{"unhealthy wins", []compose.Container{run("web"), {Service: "db", State: "running", Health: "unhealthy"}}, []string{"db", "web"}, true, InstUnhealthy},
		{"health starting is not a failure", []compose.Container{{Service: "web", State: "running", Health: "starting"}}, []string{"web"}, true, InstRunning},
		{"healthy", []compose.Container{{Service: "web", State: "running", Health: "healthy"}}, []string{"web"}, true, InstRunning},
		{"crashed", []compose.Container{run("web"), {Service: "db", State: "exited", ExitCode: 1}}, []string{"db", "web"}, true, InstUnhealthy},
		{"all crashed", []compose.Container{{Service: "web", State: "exited", ExitCode: 137}}, []string{"web"}, true, InstUnhealthy},
		{"restarting", []compose.Container{{Service: "web", State: "restarting"}}, []string{"web"}, true, InstUnhealthy},
		{"paused beside running", []compose.Container{run("web"), {Service: "db", State: "paused"}}, []string{"db", "web"}, true, InstPartial},
		{"two replicas, one stopped", []compose.Container{run("web"), {Service: "web", State: "exited"}}, []string{"web"}, true, InstRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InstanceState(tt.containers, tt.expected, tt.dockerOK); got != tt.want {
				t.Errorf("InstanceState = %q, want %q", got, tt.want)
			}
		})
	}
}
