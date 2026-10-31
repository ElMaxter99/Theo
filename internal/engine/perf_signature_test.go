package engine

import (
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

func TestSnapshotSignatureSeesWhatTheOverviewShowsAndNothingElse(t *testing.T) {
	web := compose.Container{Name: "a-web-1", Service: "web", State: "running", Publishers: []compose.Publisher{{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"}}}
	db := compose.Container{Name: "a-db-1", Service: "db", State: "running"}
	other := compose.Container{Name: "b-web-1", Service: "web", State: "running"}
	base := (&DockerSnapshot{byProject: map[string][]compose.Container{"a": {web, db}, "b": {other}}}).Signature([]string{"A"})

	same := (&DockerSnapshot{byProject: map[string][]compose.Container{"a": {db, web}, "b": {{Name: "b-web-1", Service: "web", State: "exited"}}}}).Signature([]string{"a"})
	if same != base {
		t.Error("the order of containers and changes in other projects must not change the signature")
	}
	for name, c := range map[string]compose.Container{
		"state":   {Name: "a-web-1", Service: "web", State: "exited", ExitCode: 1, Publishers: web.Publishers},
		"health":  {Name: "a-web-1", Service: "web", State: "running", Health: "unhealthy", Publishers: web.Publishers},
		"port":    {Name: "a-web-1", Service: "web", State: "running", Publishers: []compose.Publisher{{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 8081, Protocol: "tcp"}}},
		"missing": {},
	} {
		list := []compose.Container{db}
		if name != "missing" {
			list = append(list, c)
		}
		got := (&DockerSnapshot{byProject: map[string][]compose.Container{"a": list}}).Signature([]string{"a"})
		if got == base {
			t.Errorf("a change of %s must change the signature", name)
		}
	}
}
