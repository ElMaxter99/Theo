package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// Volume statuses.
const (
	VolumeInUse    = "in use"   // a container, running or stopped, mounts it
	VolumeKept     = "kept"     // no container mounts it: its data was kept after `down`
	VolumeExternal = "external" // declared external: Theo never touches it
)

// VolumeInfo is a volume of an instance.
type VolumeInfo struct {
	Name    string   `json:"name"`             // Docker name
	Volume  string   `json:"volume,omitempty"` // name in the Compose file
	Status  string   `json:"status"`
	Users   []string `json:"containers,omitempty"` // containers that mount it
	Created bool     `json:"created"`              // false for an external volume that is declared but not found
}

// Volumes lists the named volumes of an instance: the ones Compose created
// for its project, with whether a container uses them or their data was kept
// after `down`, and the external ones the project declares, which Theo never
// touches.
func (e *Engine) Volumes(ctx context.Context, inst *Instance) ([]VolumeInfo, error) {
	existing, err := e.compose.Volumes(ctx, inst.Compose)
	if err != nil {
		return nil, err
	}
	var out []VolumeInfo
	for _, v := range existing {
		users, err := e.compose.VolumeUsers(ctx, v.Name)
		if err != nil {
			return nil, err
		}
		info := VolumeInfo{Name: v.Name, Volume: v.Labels[compose.LabelVolume], Status: VolumeKept, Users: users, Created: true}
		if len(users) > 0 {
			info.Status = VolumeInUse
		}
		out = append(out, info)
	}
	for _, key := range compose.SortedKeys(inst.config.Volumes) {
		if vc := inst.config.Volumes[key]; vc.IsExternal() {
			name := vc.Name
			if name == "" {
				name = key
			}
			out = append(out, VolumeInfo{Name: name, Volume: key, Status: VolumeExternal})
		}
	}
	return out, nil
}

// PurgeError is returned when volumes cannot be deleted yet. Nothing was
// deleted.
type PurgeError struct {
	Instance string
	Reason   string
	Volumes  []VolumeInfo // what would be deleted
}

func (p *PurgeError) Error() string { return p.Reason }

// PurgeVolumes deletes the data of an instance: the named volumes Compose
// created for its project. It is a separate operation from `down`, and it
// refuses to run while the instance has containers (running or stopped), and
// unless confirm equals the instance's Compose project name. External
// volumes and volumes of other instances are never included. It returns what
// it deleted.
func (e *Engine) PurgeVolumes(ctx context.Context, inst *Instance, confirm string) ([]VolumeInfo, error) {
	doomed, err := e.purgePlan(ctx, inst, confirm)
	if err != nil || len(doomed) == 0 {
		return nil, err
	}
	return e.deleteVolumes(ctx, inst, doomed)
}

// purgePlan checks that a purge may go ahead and returns what it would
// delete. Nothing is deleted.
func (e *Engine) purgePlan(ctx context.Context, inst *Instance, confirm string) ([]VolumeInfo, error) {
	all, err := e.Volumes(ctx, inst)
	if err != nil {
		return nil, err
	}
	var doomed []VolumeInfo
	for _, v := range all {
		if v.Status != VolumeExternal {
			doomed = append(doomed, v)
		}
	}
	if len(doomed) == 0 {
		return nil, nil
	}
	containers, err := e.compose.PS(ctx, inst.target())
	if err != nil {
		return nil, err
	}
	if len(containers) > 0 {
		return nil, &PurgeError{Instance: inst.Compose, Volumes: doomed,
			Reason: fmt.Sprintf("%s still has %d container(s); remove them first with `theo down` (its volumes are kept)", inst.Compose, len(containers))}
	}
	for _, v := range doomed {
		if v.Status == VolumeInUse {
			return nil, &PurgeError{Instance: inst.Compose, Volumes: doomed,
				Reason: fmt.Sprintf("volume %s is still used by %s; nothing was deleted", v.Name, strings.Join(v.Users, ", "))}
		}
	}
	if confirm != inst.Compose {
		return nil, &PurgeError{Instance: inst.Compose, Volumes: doomed,
			Reason: fmt.Sprintf("this deletes the data of %d volume(s) of %s for good; nothing was deleted", len(doomed), inst.Compose)}
	}
	return doomed, nil
}

func (e *Engine) deleteVolumes(ctx context.Context, inst *Instance, doomed []VolumeInfo) ([]VolumeInfo, error) {
	var removed []VolumeInfo
	for _, v := range doomed {
		if err := e.compose.RemoveVolume(ctx, v.Name); err != nil {
			e.record(inst, "volumes purge", err)
			return removed, fmt.Errorf("deleted %d of %d volumes; stopped at %s: %w", len(removed), len(doomed), v.Name, err)
		}
		removed = append(removed, v)
	}
	e.record(inst, "volumes purge", nil)
	return removed, nil
}
