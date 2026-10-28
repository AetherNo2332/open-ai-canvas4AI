package commands

import (
	"reflect"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Undo changes authoring and an explicit ready selection, never a real task's
// lifecycle. In particular a completed task cannot become pending again.
func reconcileHistoryMedia(tx *repository.Repository, owner, canvas, epoch string, birth int64, before, after Document, recreated map[string]bool) ([]Operation, error) {
	allowed := []Operation{}
	for _, node := range maps(after["nodes"]) {
		id := text(node["id"])
		old := entity(before, "node:"+id)
		wanted := mediaversions.ReadState(node)
		if old == nil || recreated["node:"+id] {
			if wanted.Epoch != "" {
				scope := mediaversions.Scope{OwnerID: owner, CanvasID: canvas, Epoch: epoch, NodeID: id, Generation: birth}
				if err := mediaversions.Rebase(tx, scope, node); err != nil {
					return nil, PersistenceError(err)
				}
				allowed = append(allowed, Operation{Type: "use_media_version", ID: id})
			}
			continue
		}
		live := mediaversions.ReadState(old)
		if live.Epoch == "" {
			if wanted.CurrentVersionID != "" {
				scope, err := mediaversions.ScopeForNode(tx, owner, canvas, id)
				if err != nil {
					return nil, PersistenceError(err)
				}
				if err = mediaversions.Rebase(tx, scope, node); err != nil {
					return nil, PersistenceError(err)
				}
				allowed = append(allowed, Operation{Type: "use_media_version", ID: id})
			}
			continue
		}
		if reflect.DeepEqual(model.CanvasMediaProjection(old), model.CanvasMediaProjection(node)) {
			continue
		}
		m, _ := node["metadata"].(map[string]any)
		if m == nil {
			m = map[string]any{}
			node["metadata"] = m
		}
		projection := model.CanvasMediaProjection(old)
		for _, key := range append(append([]string{}, model.CanvasMediaFields...), "mediaVersion", "status", "errorDetails") {
			delete(m, key)
		}
		node["type"] = old["type"]
		for key, value := range projection {
			if key != "type" {
				m[key] = value
			}
		}
		if wanted.CurrentVersionID != "" {
			scope := mediaversions.Scope{OwnerID: owner, CanvasID: canvas, Epoch: live.Epoch, NodeID: id, Generation: live.Generation}
			source := scope
			source.Epoch = wanted.Epoch
			source.Generation = wanted.Generation
			if err := mediaversions.UseHistorical(tx, scope, node, source, wanted.CurrentVersionID); err != nil {
				return nil, PersistenceError(err)
			}
			allowed = append(allowed, Operation{Type: "use_media_version", ID: id, VersionID: wanted.CurrentVersionID})
		}
	}
	return allowed, nil
}

// Only a successful same-client undo/redo uses this comparison to advance its
// neighbor's precondition. External edits still fail the entity-version guard.
func historyEntityValue(value, other map[string]any) map[string]any {
	if value == nil || other == nil || (mediaversions.ReadState(value).Epoch == "" && mediaversions.ReadState(other).Epoch == "") {
		return value
	}
	raw, _ := cloneDocument(Document(value))
	out := map[string]any(raw)
	m, _ := out["metadata"].(map[string]any)
	for _, key := range append(append([]string{}, model.CanvasMediaFields...), "mediaVersion", "status", "errorDetails") {
		delete(m, key)
	}
	return out
}
