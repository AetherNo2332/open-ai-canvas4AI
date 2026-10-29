package repository

import (
	"encoding/json"
	"reflect"
	"yingce/backend/internal/model"
)

// This monotonic fence is recorded in the same durable document transaction.
// Restoring identical IDs cannot erase a pending attempt's observed loss of
// ownership; it also covers legacy PUT rather than trusting its caller shape.
func (r *Repository) FenceChangedMediaOutputOwners(owner, canvas, epoch string, before, after []map[string]any, recreated map[string]bool) error {
	if !r.db.Migrator().HasColumn(&model.CanvasTaskBinding{}, "MediaVersionJSON") {
		return nil
	}
	bindings, err := r.ActiveCanvasTaskBindings(owner, canvas, epoch)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.MediaVersionJSON == "" {
			continue
		}
		var base map[string]any
		if err := json.Unmarshal([]byte(binding.MediaVersionJSON), &base); err != nil {
			return err
		}
		nodeID, _ := base["nodeId"].(string)
		if nodeID == "" || base["blocked"] != nil {
			continue
		}
		old, next := model.CanvasMediaOutputOwners(before, nodeID), model.CanvasMediaOutputOwners(after, nodeID)
		changed := !reflect.DeepEqual(old, next)
		for _, slot := range old {
			if recreated["node:"+slot.NodeID] {
				changed = true
			}
		}
		if !changed {
			continue
		}
		base["blocked"] = "output_binding_changed"
		raw, err := json.Marshal(base)
		if err != nil {
			return err
		}
		if err = r.UpdateCanvasTaskBindingMediaVersion(binding.TaskID, owner, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) fenceLegacyMediaOutputOwners(owner, canvas, epoch string, before, after map[string]any) error {
	parse := func(doc map[string]any) []map[string]any {
		raw, _ := json.Marshal(doc["nodes"])
		var nodes []map[string]any
		_ = json.Unmarshal(raw, &nodes)
		return nodes
	}
	return r.FenceChangedMediaOutputOwners(owner, canvas, epoch, parse(before), parse(after), nil)
}
