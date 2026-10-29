package commands

import (
	"context"
	"reflect"

	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// ReadMediaVersions 返回节点 metadata.mediaVersions 的全部版本（不超过 30 条，不分页）。
func (s *Service) ReadMediaVersions(ctx context.Context, p identity.Principal, canvas, nodeID string) (mediaversions.Page, error) {
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
		out, err = mediaversions.Read(tx, scope, node)
		return PersistenceError(err)
	})
	return out, err
}

// applyMediaVersionOperation 执行 Agent 的版本操作；语义与浏览器 adoptMediaVersion /
// createNodeFromMediaVersion 一致（同一 Go 实现由共享夹具对齐）。
func (p *planner) applyMediaVersionOperation(doc Document, op Operation) ([]OperationSummary, error) {
	nodes := maps(doc["nodes"])
	index := p.canvasNodeIndex(nodes, op.ID)
	if index < 0 {
		return nil, failure("entity_deleted", "目标节点不存在")
	}
	node := nodes[index]
	switch op.Type {
	case "use_media_version":
		if mediaversions.IsCurrent(node, op.VersionID) && mediaversions.AdoptBlock(node) == "" {
			return nil, nil
		}
		adopted, err := mediaversions.Adopt(node, op.VersionID)
		if err != nil {
			return nil, PersistenceError(err)
		}
		nodes[index] = adopted
		doc["nodes"] = p.mapsAsAny(nodes)
		return nil, nil
	case "extract_media_version":
		if err := validateCanvasID(op.SourceNodeID, "取出版本的新节点 ID", 80); err != nil {
			return nil, err
		}
		if p.canvasNodeIndex(nodes, op.SourceNodeID) >= 0 {
			return nil, BadAuthRequest("新节点 ID 已存在")
		}
		extracted, err := mediaversions.Extract(node, op.VersionID, op.SourceNodeID)
		if err != nil {
			return nil, PersistenceError(err)
		}
		doc["nodes"] = p.mapsAsAny(append(nodes, extracted))
		return []OperationSummary{{Operation: op.Type, NodeID: op.SourceNodeID, NodeTitle: text(extracted["title"]), NodeType: text(extracted["type"])}}, nil
	}
	return nil, BadAuthRequest("未知的媒体版本操作")
}

// validateManagedMedia 守住 Agent 通用操作：有版本历史的节点只能经版本操作或任务回写更换媒体，
// 通用操作也不能直接写版本字段。版本操作本身由 Go 版上游函数完成。
func validateManagedMedia(before, after Document, ops []Operation) error {
	allowed := map[string]bool{}
	for _, op := range ops {
		if op.Type == "use_media_version" {
			allowed[op.ID] = true
		}
		if op.Type == "extract_media_version" {
			allowed[op.SourceNodeID] = true
		}
	}
	for _, node := range maps(after["nodes"]) {
		id := text(node["id"])
		if allowed[id] {
			continue
		}
		newMeta, _ := node["metadata"].(map[string]any)
		old := entity(before, "node:"+id)
		if old == nil {
			if newMeta["mediaVersions"] != nil || newMeta["currentMediaVersionId"] != nil {
				return failure("media_version_managed", "新节点不能继承其他节点的媒体版本")
			}
			continue
		}
		oldMeta, _ := old["metadata"].(map[string]any)
		if !reflect.DeepEqual(oldMeta["mediaVersions"], newMeta["mediaVersions"]) || !reflect.DeepEqual(oldMeta["currentMediaVersionId"], newMeta["currentMediaVersionId"]) {
			return failure("media_version_managed", "媒体版本由版本操作管理，请使用 use_media_version 或 extract_media_version")
		}
		if len(mediaversions.List(old)) > 0 && !reflect.DeepEqual(model.CanvasMediaProjection(old), model.CanvasMediaProjection(node)) {
			return failure("media_version_managed", "媒体由版本历史管理，请通过 use_media_version 选择；生成中请查询原任务")
		}
	}
	return nil
}

// validateVersionRecords 与浏览器保存守卫同一规则：已有版本记录的媒体不可被改写，
// 固定到 versionId 的引用因此始终解析到同一份媒体。
func validateVersionRecords(before, after Document) error {
	if model.CanvasMediaVersionRecordChanged(before, after) {
		return failure("media_version_managed", "媒体版本记录不可改写")
	}
	return nil
}

func managedMediaSnapshot(doc Document) Document {
	raw, _ := cloneDocument(doc)
	return raw
}
