package app

import (
	"errors"
	"fmt"
	"sort"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/repository"
)

func cloudAgentMediaVersionTargetIssue(repo *repository.Repository, userID, canvasID string, node map[string]any, args cloudAgentMediaArgs) (string, string) {
	if stringValue(node["type"]) != args.Mode {
		return "target_type_mismatch", "目标节点类型与生成模式不匹配"
	}
	scope, err := mediaversions.ScopeForNode(repo, userID, canvasID, args.NodeID)
	if err != nil && repo.IsMissing(err) && args.Intent != "new_version" {
		scope, err = mediaversions.Scope{OwnerID: userID, CanvasID: canvasID, NodeID: args.NodeID}, nil
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

// cloudAgentMediaVersionError 携带 Agent 合同里的版本操作失败原因。
type cloudAgentMediaVersionError struct {
	error
	reason string
}

func (e *cloudAgentMediaVersionError) Unwrap() error { return e.error }

// cloudAgentMediaVersionRetryable 对应决定 2：只有上传中或首次生成中可以稍后重试。
func cloudAgentMediaVersionRetryable(reason string) bool { return reason == "media_version_loading" }

// applyCloudAgentMediaVersionOp 用与浏览器一致的 Go 版上游函数执行 use_media_version / extract_media_version。
func applyCloudAgentMediaVersionOp(nodes []map[string]any, index int, op agentCanvasOp) (cloudAgentApprovalPreviewItem, []map[string]any, error) {
	node := nodes[index]
	title := stringValue(node["title"])
	wrap := func(err error) error {
		var e *mediaversions.Error
		if errors.As(err, &e) {
			return &cloudAgentMediaVersionError{reason: e.Reason, error: BadAuthRequest(e.Message)}
		}
		return err
	}
	switch op.Type {
	case "use_media_version":
		if mediaversions.IsCurrent(node, op.VersionID) && mediaversions.AdoptBlock(node) == "" {
			return cloudAgentApprovalPreviewItem{Operation: op.Type, NodeID: op.ID, NodeTitle: title, Summary: "已是当前版本"}, nodes, nil
		}
		adopted, err := mediaversions.Adopt(node, op.VersionID)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, nil, wrap(err)
		}
		nodes[index] = adopted
		number := mediaversions.Find(adopted, op.VersionID)["number"]
		return cloudAgentApprovalPreviewItem{Operation: op.Type, NodeID: op.ID, NodeTitle: title, Summary: fmt.Sprintf("切换到 V%v", number)}, nodes, nil
	case "extract_media_version":
		if err := validateCloudAgentID(op.NodeID, "取出版本的新节点 ID", 80); err != nil {
			return cloudAgentApprovalPreviewItem{}, nil, err
		}
		if cloudAgentNodeIndex(nodes, op.NodeID) >= 0 {
			return cloudAgentApprovalPreviewItem{}, nil, BadAuthRequest("取出版本的新节点 ID 已存在")
		}
		extracted, err := mediaversions.Extract(node, op.VersionID, op.NodeID)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, nil, wrap(err)
		}
		return cloudAgentApprovalPreviewItem{Operation: op.Type, NodeID: op.NodeID, NodeTitle: stringValue(extracted["title"]), NodeType: stringValue(extracted["type"]), TargetNodeID: op.ID, TargetNodeTitle: title, Summary: "取出版本为独立节点"}, append(nodes, extracted), nil
	}
	return cloudAgentApprovalPreviewItem{}, nil, BadAuthRequest("不支持的媒体版本操作")
}

func cloudAgentMediaPreviewContext(plan *cloudAgentMediaPlan, doc map[string]any) {
	if plan.Args.Intent != "new_version" {
		return
	}
	nodes, _ := creationObjects(doc["nodes"])
	node := nodes[plan.Args.NodeID]
	plan.CurrentTitle = stringValue(node["title"])
	plan.CurrentVersion = "当前媒体"
	if current := mediaversions.Current(node); current != nil {
		plan.CurrentVersion = fmt.Sprintf("V%v", current["number"])
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
