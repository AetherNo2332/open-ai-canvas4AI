package tasks

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) captureEmotion(ctx context.Context, tx *repository.Repository, p identity.Principal, doc commands.Document, node map[string]any, req SubmitRequest) (string, map[string]bool, error) {
	if req.Task.Mode != "image" || (req.Consumer.Kind != "" && req.Consumer.Kind != "node") {
		return "", nil, nil
	}
	emotion, ok := metadata(node)["emotionEdit"].(map[string]any)
	if !ok {
		return "", nil, nil
	}
	sourceID := str(emotion["sourceNodeId"])
	source := findNode(doc, sourceID)
	if source == nil {
		return "", nil, commands.BadAuthRequest("表情编辑源节点不存在")
	}
	m := metadata(source)
	id := assets.ResourceID(str(m["storageKey"]))
	if id == "" {
		id = assets.ResourceID(str(m["content"]))
	}
	if id == "" {
		return "", nil, commands.BadAuthRequest("表情编辑源图片尚未保存为资源")
	}
	raw, err := json.Marshal(map[string]any{"sourceResourceId": id, "editRegion": emotion["editRegion"], "faceBox": emotion["faceBox"]})
	if err != nil {
		return "", nil, err
	}
	var edit EmotionContext
	if err = json.Unmarshal(raw, &edit); err != nil || !validRegion(edit.EditRegion) || !validRegion(edit.FaceBox) {
		return "", nil, commands.BadAuthRequest("表情编辑区域无效")
	}
	reads, err := s.commands.ValidateTaskReferences(ctx, tx, p, doc, map[string]any{"sourceResourceId": id})
	if err != nil {
		return "", nil, err
	}
	reads["node:"+sourceID] = true
	return string(raw), reads, nil
}
