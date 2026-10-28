package app

import (
	"errors"
	"sort"
	"strconv"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/repository"
)

func cloudAgentMediaVersionTargetIssue(repo *repository.Repository, userID, canvasID string, node map[string]any, args cloudAgentMediaArgs) (string, string) {
	if stringValue(node["type"]) != args.Mode {
		return "target_type_mismatch", "目标节点类型与生成模式不匹配"
	}
	scope, err := mediaversions.ScopeForNode(repo, userID, canvasID, args.NodeID)
	if err != nil && repo.IsMissing(err) && args.Intent != "new_version" {
		state := mediaversions.ReadState(node)
		scope = mediaversions.Scope{OwnerID: userID, CanvasID: canvasID, NodeID: args.NodeID, Epoch: state.Epoch, Generation: state.Generation}
		err = nil
	}
	if err == nil {
		err = mediaversions.CheckAdmission(repo, scope, node, args.Intent)
	}
	if err != nil {
		var e *mediaversions.Error
		if errors.As(err, &e) {
			return e.Reason, e.Message
		}
		return "target_unavailable", "无法核对媒体版本，请重新读取节点"
	}
	return "", ""
}

func cloudAgentMediaPreviewContext(plan *cloudAgentMediaPlan, doc map[string]any) {
	if plan.Args.Intent != "new_version" {
		return
	}
	nodes, _ := creationObjects(doc["nodes"])
	node := nodes[plan.Args.NodeID]
	plan.CurrentTitle = stringValue(node["title"])
	state := mediaversions.ReadState(node)
	plan.CurrentVersion = "当前媒体"
	if state.CurrentVersion > 0 {
		plan.CurrentVersion = "V" + strconv.FormatInt(state.CurrentVersion, 10)
	}
	consumers := map[string]bool{}
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["fromNodeId"]) == plan.Args.NodeID {
			consumers[stringValue(edge["toNodeId"])] = true
		}
	}
	var references func(any) bool
	references = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			if stringValue(v["nodeId"]) == plan.Args.NodeID && stringValue(v["versionId"]) == "" && (v["resolution"] == nil || v["resolution"] == "latest") {
				return true
			}
			for key, item := range v {
				switch key {
				case "referenceNodeIds", "imageNodeId", "videoNodeId", "outputNodeId":
					if item == plan.Args.NodeID {
						return true
					}
					if values, ok := item.([]any); ok {
						for _, id := range values {
							if id == plan.Args.NodeID {
								return true
							}
						}
					}
				}
				if references(item) {
					return true
				}
			}
		case []any:
			for _, item := range v {
				if references(item) {
					return true
				}
			}
		}
		return false
	}
	for id, candidate := range nodes {
		if id != plan.Args.NodeID && references(candidate["metadata"]) {
			consumers[id] = true
		}
	}
	ids := []string{}
	for id := range consumers {
		if id != "" && id != plan.Args.NodeID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		title := stringValue(nodes[id]["title"])
		if title == "" {
			title = id
		}
		plan.Consumers = append(plan.Consumers, truncateRunes(title, 80))
	}
}
