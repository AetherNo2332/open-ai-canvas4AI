package commands

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/mediaversions"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"reflect"
)

func (s *Service) ReadMediaVersions(ctx context.Context, p identity.Principal, canvas, nodeID string, before int64) (mediaversions.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out mediaversions.Page
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvas, "canvas.read"); err != nil {
			return err
		}
		_, _, _, doc, err := s.state(tx, p, canvas)
		if err != nil {
			return err
		}
		node := entity(doc, "node:"+nodeID)
		if node == nil {
			return failure("entity_deleted", "媒体节点不存在")
		}
		if !mediaversions.Supported(text(node["type"])) {
			return BadAuthRequest("目标不是媒体节点")
		}
		scope, err := mediaversions.ScopeForNode(tx, p.OwnerID, canvas, nodeID)
		if err != nil {
			return PersistenceError(err)
		}
		out, err = mediaversions.Read(tx, scope, node, before)
		return PersistenceError(err)
	})
	return out, err
}

// Managed media may only change through task materialization or the explicit
// version-selection command; geometry and authoring fields stay editable.
func validateManagedMedia(before, after Document, ops []Operation) error {
	allowed := map[string]bool{}
	for _, op := range ops {
		if op.Type == "use_media_version" {
			allowed[op.ID] = true
		}
	}
	for _, node := range maps(after["nodes"]) {
		id := text(node["id"])
		old := entity(before, "node:"+id)
		if old == nil {
			if m, _ := node["metadata"].(map[string]any); m["mediaVersion"] != nil && !allowed[id] {
				return failure("media_version_managed", "新节点不能继承其他节点的媒体版本")
			}
			continue
		}
		oldMeta, _ := old["metadata"].(map[string]any)
		newMeta, _ := node["metadata"].(map[string]any)
		if oldMeta["mediaVersion"] == nil && newMeta["mediaVersion"] != nil && !allowed[id] {
			return failure("media_version_managed", "媒体版本身份由服务端管理")
		}
		if oldMeta["mediaVersion"] != nil && !allowed[id] && !reflect.DeepEqual(model.CanvasMediaProjection(old), model.CanvasMediaProjection(node)) {
			return failure("media_version_managed", "媒体由版本记录管理，请通过版本菜单选择；生成中请查询原任务")
		}
	}
	return nil
}

func managedMediaSnapshot(doc Document) Document {
	nodes := []any{}
	for _, node := range maps(doc["nodes"]) {
		if m, _ := node["metadata"].(map[string]any); m["mediaVersion"] != nil {
			projection := model.CanvasMediaProjection(node)
			kind := projection["type"]
			delete(projection, "type")
			nodes = append(nodes, map[string]any{"id": node["id"], "type": kind, "metadata": projection})
		}
	}
	raw, _ := json.Marshal(Document{"nodes": nodes})
	var out Document
	_ = json.Unmarshal(raw, &out)
	return out
}
