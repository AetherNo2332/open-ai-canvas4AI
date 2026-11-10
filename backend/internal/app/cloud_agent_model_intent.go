package app

import (
	"strconv"

	"yingce/backend/internal/repository"
)

// Resolve actual canvas resources before filtering the shared catalog. Counts
// supplied by the model must not replace resource ownership/readiness checks.
func (s *Service) cloudAgentModelIntent(userID, canvasID, arguments string) (*ModelRequestIntent, error) {
	var a struct {
		Mode             string   `json:"mode"`
		ReferenceNodeIDs []string `json:"referenceNodeIds"`
		StoryboardNodeID string   `json:"storyboardNodeId"`
		StoryboardRowID  string   `json:"storyboardRowId"`
	}
	if err := decodeCloudAgentJSONObject(arguments, &a); err != nil {
		return nil, BadAuthRequest("模型查询参数无效")
	}
	if a.Mode == "" && len(a.ReferenceNodeIDs) == 0 && a.StoryboardNodeID == "" && a.StoryboardRowID == "" {
		return nil, nil
	}
	if !cloudAgentGenerationModeSupported(a.Mode) || len(a.ReferenceNodeIDs) > 16 {
		return nil, BadAuthRequest("请指定支持的生成模式，参考节点最多16个")
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	return s.cloudAgentModelIntentFromDocument(userID, canvas.ProjectID, arguments, doc)
}

func (s *Service) cloudAgentModelIntentFromDocument(userID, projectID, arguments string, doc map[string]any) (*ModelRequestIntent, error) {
	rowDuration := 0
	var a struct {
		Mode             string   `json:"mode"`
		ReferenceNodeIDs []string `json:"referenceNodeIds"`
		StoryboardNodeID string   `json:"storyboardNodeId"`
		StoryboardRowID  string   `json:"storyboardRowId"`
	}
	if err := decodeCloudAgentJSONObject(arguments, &a); err != nil {
		return nil, BadAuthRequest("模型查询参数无效")
	}
	if a.Mode == "" && len(a.ReferenceNodeIDs) == 0 && a.StoryboardNodeID == "" && a.StoryboardRowID == "" {
		return nil, nil
	}
	if !cloudAgentGenerationModeSupported(a.Mode) || len(a.ReferenceNodeIDs) > 16 {
		return nil, BadAuthRequest("请指定支持的生成模式，参考节点最多16个")
	}
	if a.StoryboardNodeID != "" || a.StoryboardRowID != "" {
		referenceNodeIDs, duration, err := cloudAgentResolveModelIntentStoryboard(doc, arguments, a.Mode, a.StoryboardNodeID, a.StoryboardRowID)
		if err != nil {
			return nil, err
		}
		a.ReferenceNodeIDs = referenceNodeIDs
		rowDuration = duration
	}
	refs, err := cloudAgentCollectModelIntentReferences(s.repo, userID, projectID, doc, a.ReferenceNodeIDs)
	if err != nil {
		return nil, err
	}
	if err := validateCloudAgentMediaReferences(a.Mode, refs); err != nil {
		return nil, err
	}
	refs["mode"] = a.Mode
	if a.Mode == "video" && rowDuration > 0 {
		refs["config"] = map[string]any{"videoSeconds": strconv.Itoa(rowDuration)}
	}
	operation := cloudAgentMediaOperation(a.Mode, refs)
	if a.Mode == "video" {
		operation = ""
	}
	intent := ModelRequestIntentFromTaskInput(refs, "canvas_"+a.Mode, operation)
	return &intent, nil
}

// cloudAgentResolveModelIntentStoryboard 解析分镜行输入：校验参数冲突并解析出
// 参考节点与行时长。
func cloudAgentResolveModelIntentStoryboard(doc map[string]any, arguments, mode, storyboardNodeID, storyboardRowID string) ([]string, int, error) {
	args := cloudAgentMediaArgs{Mode: mode, StoryboardNodeID: storyboardNodeID, StoryboardRowID: storyboardRowID}
	if err := validateCloudAgentStoryboardInput(arguments, args); err != nil {
		return nil, 0, err
	}
	resolved, _, err := resolveCloudAgentStoryboardGeneration(doc, args)
	if err != nil {
		return nil, 0, err
	}
	return resolved.ReferenceNodeIDs, resolved.Duration, nil
}

// cloudAgentCollectModelIntentReferences 逐个解析参考节点为媒体引用；空 ID、
// 重复或不存在的节点直接拒绝。
func cloudAgentCollectModelIntentReferences(repo *repository.Repository, userID, projectID string, doc map[string]any, referenceNodeIDs []string) (map[string]any, error) {
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		return nil, err
	}
	refs := map[string]any{}
	seen := map[string]bool{}
	for _, id := range referenceNodeIDs {
		if id == "" || seen[id] || nodes[id] == nil {
			return nil, BadAuthRequest("参考节点不存在或重复")
		}
		seen[id] = true
		ref, field, err := cloudAgentMediaReference(repo, userID, projectID, nodes[id])
		if err != nil {
			return nil, err
		}
		list, _ := refs[field].([]any)
		refs[field] = append(list, ref)
	}
	return refs, nil
}
