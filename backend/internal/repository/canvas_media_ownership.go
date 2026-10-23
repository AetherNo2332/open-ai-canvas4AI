package repository

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"reflect"
)

// This monotonic fence is recorded in the same durable document transaction.
// Restoring identical IDs cannot erase a pending attempt's observed loss of
// ownership; it also covers legacy PUT rather than trusting its caller shape.
func (r *Repository) FenceChangedMediaOutputOwners(owner, canvas, epoch string, before, after []map[string]any, recreated map[string]bool) error {
	if !r.mediaVersionsEnabled {
		return nil
	}
	var pending []model.CanvasMediaVersion
	if err := r.db.Select("id", "node_id", "auto_adopt_blocked_reason").Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND status IN ?", owner, canvas, epoch, []string{"queued", "running", "submission_uncertain", "materializing"}).Find(&pending).Error; err != nil {
		return err
	}
	ids := []string{}
	for _, version := range pending {
		if version.AutoAdoptBlockedReason != "" {
			continue
		}
		old, next := model.CanvasMediaOutputOwners(before, version.NodeID), model.CanvasMediaOutputOwners(after, version.NodeID)
		changed := !reflect.DeepEqual(old, next)
		for _, slot := range old {
			if recreated["node:"+slot.NodeID] {
				changed = true
			}
		}
		if changed {
			ids = append(ids, version.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.CanvasMediaVersion{}).Where("owner_id = ? AND id IN ?", owner, ids).Update("auto_adopt_blocked_reason", "output_binding_changed").Error
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
