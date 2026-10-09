package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

func cloudAgentListAssets(repo *repository.Repository, userID string, call cloudAgentCall) (any, error) {
	var args struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Page  int    `json:"page"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if args.Page == 0 {
		args.Page = 1
	}
	if args.Page < 1 || args.Page > 10000 || len([]rune(args.Query)) > 500 {
		return nil, BadAuthRequest("素材查询参数无效")
	}
	switch args.Kind {
	case "", "image", "video", "audio", "text":
	default:
		return nil, BadAuthRequest("素材类型不受支持")
	}
	rows, total, err := repo.UserAssetsPage(userID, args.Page, 40, repository.UserAssetPageFilter{Query: args.Query, Kind: args.Kind, Status: "active"})
	if err != nil {
		return nil, err
	}
	result := []any{}
	for _, asset := range rows {
		result = append(result, map[string]any{"assetId": asset.ID, "kind": asset.Kind, "title": asset.Title})
	}
	return map[string]any{"assets": result, "page": args.Page, "hasMore": int64(args.Page*40) < total, "totalAssets": total}, nil
}

func prepareCloudAgentAssetBinding(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentCanvasMutationPlan, error) {
	var args struct {
		SnapshotHash string `json:"snapshotHash"`
		NodeID       string `json:"nodeId"`
		AssetID      string `json:"assetId"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	hash := cloudAgentCanvasHash(doc)
	if hash != args.SnapshotHash {
		return nil, cloudAgentSnapshotConflictError("画布已变化，请重新读取后绑定素材")
	}
	nodes := creationMaps(doc["nodes"])
	index := cloudAgentNodeIndex(nodes, args.NodeID)
	if index < 0 {
		return nil, BadAuthRequest("绑定节点不在当前画布")
	}
	node := nodes[index]
	if descriptor, ok := cloudAgentNodeCapabilityForNode(node); !ok || descriptor.Variant != nil {
		return nil, BadAuthRequest("角色卡或未知节点不能替换为普通素材")
	}
	kind := stringValue(node["type"])
	if kind != "image" && kind != "video" && kind != "audio" && kind != "text" {
		return nil, BadAuthRequest("当前节点不支持素材库内容替换")
	}
	asset, err := repo.AssetForUser(userID, args.AssetID)
	if err != nil {
		return nil, err
	}
	if asset.Kind != kind || asset.Status == model.AssetVersionStatusArchived {
		return nil, BadAuthRequest("素材类型与节点不匹配或已归档")
	}
	metadata := cloudAgentNodeMetadata(node)
	if status := stringValue(metadata["status"]); status == "loading" || status == "running" || status == "pending" {
		return nil, BadAuthRequest("请等待节点任务结束后替换素材")
	}
	var payload map[string]any
	if err = json.Unmarshal([]byte(asset.PayloadJSON), &payload); err != nil {
		return nil, BadAuthRequest("素材记录无效")
	}
	data, _ := payload["data"].(map[string]any)
	if kind == "text" {
		content, ok := data["content"].(string)
		if !ok {
			content, ok = data["text"].(string)
		}
		if !ok || len([]rune(content)) > 1<<20 {
			return nil, BadAuthRequest("文本素材内容无效")
		}
		metadata["content"] = content
		delete(metadata, "richText")
	} else {
		resourceID := assets.ResourceID(stringValue(data["storageKey"]))
		if resourceID == "" {
			for _, field := range []string{"dataUrl", "url"} {
				resourceID = assets.ResourceID(stringValue(data[field]))
				if resourceID != "" {
					break
				}
			}
		}
		if resourceID == "" {
			return nil, BadAuthRequest("素材没有可绑定的平台资源")
		}
		resource, err := repo.ResourceForUser(userID, resourceID)
		if err != nil {
			return nil, err
		}
		if resource.Status != model.ResourceStatusReady || !(resource.Kind == kind || strings.HasPrefix(resource.MimeType, kind+"/")) {
			return nil, BadAuthRequest("素材资源未就绪或类型不符")
		}
		cloudAgentResetCopiedMetadata(metadata, nil)
		for _, field := range []string{"agentGenerationContinuation", "agentDraftRunId", "agentDraftCallId", "generationSubmitted", "generationEffect", "generationEffects", "generationEffectKeys", "producedModel", "previewContent", "previewStorageKey", "videoPreview", "durationMs", "hasAudio", "naturalWidth", "naturalHeight", "intrinsicWidth", "intrinsicHeight", "bytes", "mimeType"} {
			delete(metadata, field)
		}
		metadata["content"], metadata["storageKey"] = "/api/resources/"+resource.ID+"/file", "resource:"+resource.ID
		metadata["status"] = "success"
		delete(metadata, "errorDetails")
		for _, field := range []string{"taskId", "taskStatus", "taskProgress", "taskError", "generationAppliedKeys", "generationPersistence"} {
			delete(metadata, field)
		}
		metadata["mimeType"], metadata["bytes"] = resource.MimeType, resource.Size
		if resource.DurationMs > 0 {
			metadata["durationMs"] = resource.DurationMs
		}
		if resource.Width > 0 {
			metadata["naturalWidth"] = resource.Width
		}
		if resource.Height > 0 {
			metadata["naturalHeight"] = resource.Height
		}
	}
	metadata["assetId"] = asset.ID
	doc["nodes"] = mapsAsAny(nodes)
	preview := cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "替换节点素材", Description: "使用当前用户素材库中的内容替换目标节点。", Items: []cloudAgentApprovalPreviewItem{{Operation: "bind_asset", NodeID: args.NodeID, NodeType: kind, NodeTitle: stringValue(node["title"]), Summary: "绑定素材《" + asset.Title + "》"}}}
	return &cloudAgentCanvasMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: hash, Preview: preview}, nil
}

func applyCloudAgentAssetBinding(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder cloudAgentMutationRecorder) (any, error) {
	plan, err := prepareCloudAgentAssetBinding(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if recorder != nil {
		if err := recorder(repo, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: call.Function.Name, BeforeJSON: plan.BeforeJSON, BeforeSnapshotHash: plan.BeforeSnapshotHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document), Preview: &plan.Preview}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"accepted": true, "nodeId": plan.Preview.Items[0].NodeID, "snapshotHash": cloudAgentCanvasHash(plan.Document), "preview": plan.Preview}, nil
}
