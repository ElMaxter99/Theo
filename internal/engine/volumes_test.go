package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

func volumesFixture(t *testing.T) (*Engine, *fakeCompose, *Instance) {
	t.Helper()
	e, c, inst := portsFixture(t, map[string]compose.Service{"api": {}})
	inst.config.Volumes = map[string]compose.VolumeConfig{
		"data":   {},
		"shared": {Name: "gr-shared", External: json.RawMessage("true")},
	}
	vol := func(name, key string) compose.DockerVolume {
		return compose.DockerVolume{Name: name, Labels: map[string]string{compose.LabelVolume: key}}
	}
	c.volumes = map[string][]compose.DockerVolume{"app-app-feature": {vol("app-app-feature_data", "data")}}
	return e, c, inst
}

func TestVolumesReportsKeptInUseAndExternal(t *testing.T) {
	e, c, inst := volumesFixture(t)
	ctx := context.Background()
	list, err := e.Volumes(ctx, inst)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, v := range list {
		got = append(got, v.Name+"="+v.Status)
	}
	if want := []string{"app-app-feature_data=kept", "gr-shared=external"}; !reflect.DeepEqual(got, want) {
		t.Errorf("volumes = %v, want %v", got, want)
	}
	c.users = map[string][]string{"app-app-feature_data": {"some-container"}}
	list, _ = e.Volumes(ctx, inst)
	if list[0].Status != VolumeInUse || list[0].Users[0] != "some-container" {
		t.Errorf("in use: %+v", list[0])
	}
}

func TestPurgeVolumesNeedsEverythingToLineUp(t *testing.T) {
	e, c, inst := volumesFixture(t)
	ctx := context.Background()
	var pe *PurgeError

	// No confirmation: lists what would go, deletes nothing. External is not included.
	if _, err := e.PurgeVolumes(ctx, inst, ""); !errors.As(err, &pe) || len(pe.Volumes) != 1 || pe.Volumes[0].Name != "app-app-feature_data" {
		t.Fatalf("no confirmation: %v %+v", err, pe)
	}
	if _, err := e.PurgeVolumes(ctx, inst, "wrong"); !errors.As(err, &pe) {
		t.Errorf("wrong confirmation: %v", err)
	}

	// Containers still exist: refused even with confirmation.
	c.containers = []compose.Container{{Service: "api"}}
	if _, err := e.PurgeVolumes(ctx, inst, "app-app-feature"); !errors.As(err, &pe) || len(c.removed) != 0 {
		t.Errorf("with containers: %v removed=%v", err, c.removed)
	}
	c.containers = nil

	// A volume used by another container: refused.
	c.users = map[string][]string{"app-app-feature_data": {"other"}}
	if _, err := e.PurgeVolumes(ctx, inst, "app-app-feature"); !errors.As(err, &pe) || len(c.removed) != 0 {
		t.Errorf("used volume: %v removed=%v", err, c.removed)
	}
	c.users = nil

	removed, err := e.PurgeVolumes(ctx, inst, "app-app-feature")
	if err != nil || len(removed) != 1 || !reflect.DeepEqual(c.removed, []string{"app-app-feature_data"}) {
		t.Fatalf("purge: %v %v removed=%v", err, removed, c.removed)
	}
	if ev, _ := e.History(ctx, "app", "feature/login", 0); len(ev) != 1 || ev[0].Operation != "volumes purge" {
		t.Errorf("history: %+v", ev)
	}
}
