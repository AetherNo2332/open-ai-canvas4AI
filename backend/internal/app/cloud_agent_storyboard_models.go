package app

import "encoding/json"

// Group by actual catalog intent, never by names or model-supplied input counts.
func cloudAgentGroupStoryboardModels(rowIDs []string, resolve func(string) (*ModelRequestIntent, error), list func(*ModelRequestIntent) (any, error)) (map[string]any, error) {
	if len(rowIDs) < 1 || len(rowIDs) > 100 {
		return nil, BadAuthRequest("请选择1到100个分镜行")
	}
	seen := map[string]bool{}
	for _, id := range rowIDs {
		if id == "" || seen[id] {
			return nil, BadAuthRequest("分镜行 ID 不能为空或重复")
		}
		seen[id] = true
	}
	groups := []map[string]any{}
	indexes := map[string]int{}
	rows := []any{}
	for _, id := range rowIDs {
		intent, err := resolve(id)
		if err != nil {
			rows = append(rows, map[string]any{"rowId": id, "issue": cloudAgentSafeToolError(err)})
			continue
		}
		key, err := json.Marshal(intent)
		if err != nil {
			return nil, err
		}
		index, exists := indexes[string(key)]
		if !exists {
			models, err := list(intent)
			if err != nil {
				return nil, err
			}
			index = len(groups)
			indexes[string(key)] = index
			groups = append(groups, map[string]any{"group": index + 1, "catalog": models, "rowIds": []string{}})
		}
		groups[index]["rowIds"] = append(groups[index]["rowIds"].([]string), id)
		rows = append(rows, map[string]any{"rowId": id, "group": index + 1})
	}
	return map[string]any{"groups": groups, "rows": rows, "totalRows": len(rowIDs)}, nil
}

func (s *Service) cloudAgentQueryModels(userID, canvasID, arguments string) (any, error) {
	var args struct {
		Mode             string   `json:"mode"`
		ReferenceNodeIDs []string `json:"referenceNodeIds"`
		StoryboardNodeID string   `json:"storyboardNodeId"`
		StoryboardRowID  string   `json:"storyboardRowId"`
		StoryboardRowIDs []string `json:"storyboardRowIds"`
	}
	if err := decodeCloudAgentJSONObject(arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if args.StoryboardRowIDs == nil {
		intent, err := s.cloudAgentModelIntent(userID, canvasID, arguments)
		if err != nil {
			return nil, err
		}
		return s.cloudAgentModelList(userID, intent)
	}
	if args.StoryboardNodeID == "" || args.StoryboardRowID != "" || args.ReferenceNodeIDs != nil || (args.Mode != "image" && args.Mode != "video") {
		return nil, BadAuthRequest("多镜头选模需要分镜表、行 ID 列表及 image/video 模式，不能另传引用")
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	result, err := cloudAgentGroupStoryboardModels(args.StoryboardRowIDs, func(id string) (*ModelRequestIntent, error) {
		input, _ := json.Marshal(map[string]any{"mode": args.Mode, "storyboardNodeId": args.StoryboardNodeID, "storyboardRowId": id})
		return s.cloudAgentModelIntentFromDocument(userID, canvas.ProjectID, string(input), doc)
	}, func(intent *ModelRequestIntent) (any, error) { return s.cloudAgentModelList(userID, intent) })
	if err == nil {
		result["snapshotHash"] = cloudAgentCanvasHash(doc)
		result["storyboardNodeId"] = args.StoryboardNodeID
	}
	return result, err
}
