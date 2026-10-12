package app

import (
	"infinite-canvas/backend/internal/repository"
	"strings"
)

func cloudAgentContentRead(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	if call.Function.Name == "canvas_search_nodes" {
		var args struct {
			Query  string `json:"query"`
			Type   string `json:"type"`
			Offset int    `json:"offset"`
		}
		if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
			return nil, cloudAgentJSONArgumentError(err)
		}
		if args.Offset < 0 || len([]rune(args.Query)) > 500 {
			return nil, BadAuthRequest("搜索参数超出限制")
		}
		found := []map[string]any{}
		query := strings.ToLower(args.Query)
		for _, node := range creationMaps(doc["nodes"]) {
			if args.Type != "" && stringValue(node["type"]) != args.Type {
				continue
			}
			meta := cloudAgentNodeMetadata(node)
			text := stringValue(node["title"]) + "\n" + stringValue(meta["content"]) + "\n" + stringValue(meta["composerContent"])
			if strings.Contains(strings.ToLower(text), query) {
				found = append(found, map[string]any{"id": node["id"], "type": node["type"], "title": node["title"]})
			}
		}
		if args.Offset > len(found) {
			return nil, BadAuthRequest("搜索偏移超出结果范围")
		}
		end := min(args.Offset+40, len(found))
		return map[string]any{"nodes": found[args.Offset:end], "offset": args.Offset, "nextOffset": end, "hasMore": end < len(found), "totalNodes": len(found), "snapshotHash": cloudAgentCanvasHash(doc)}, nil
	}
	var args struct {
		NodeID string `json:"nodeId"`
		Field  string `json:"field"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	index := cloudAgentNodeIndex(creationMaps(doc["nodes"]), args.NodeID)
	if index < 0 {
		return nil, BadAuthRequest("节点不在当前画布")
	}
	node := creationMaps(doc["nodes"])[index]
	descriptor, ok := cloudAgentNodeCapabilityForNode(node)
	if !ok {
		return nil, BadAuthRequest("该节点不支持正文精读")
	}
	field, ok := descriptor.PatchFields["content"]
	if !ok || field.Kind != "string" {
		return nil, BadAuthRequest("该节点没有可编辑文本正文")
	}
	parts := strings.Split(field.Path, ".")
	if len(parts) != 2 || parts[0] != "metadata" {
		return nil, BadAuthRequest("该节点的正文路径无效")
	}
	expected := parts[1]
	if args.Field != "" && args.Field != expected {
		return nil, BadAuthRequest("请求字段不是该节点可编辑的正文")
	}
	if args.Limit == 0 {
		args.Limit = 8000
	}
	if args.Offset < 0 || args.Limit < 1 || args.Limit > 16000 {
		return nil, BadAuthRequest("正文分页参数超出限制")
	}
	text := []rune(stringValue(cloudAgentNodeMetadata(node)[expected]))
	if args.Offset > len(text) {
		return nil, BadAuthRequest("正文偏移超出长度")
	}
	end := min(args.Offset+args.Limit, len(text))
	return map[string]any{"nodeId": args.NodeID, "field": expected, "content": string(text[args.Offset:end]), "offset": args.Offset, "nextOffset": end, "hasMore": end < len(text), "totalCharacters": len(text), "snapshotHash": cloudAgentCanvasHash(doc)}, nil
}
