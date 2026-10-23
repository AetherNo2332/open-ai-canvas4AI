package app

import (
	"errors"
	"infinite-canvas/backend/internal/canvas/mediaversions"
	"infinite-canvas/backend/internal/repository"
)

func cloudAgentMediaVersionTargetIssue(repo *repository.Repository, userID, canvasID string, node map[string]any, args cloudAgentMediaArgs) (string, string) {
	if args.Intent != "new_version" {
		return "", ""
	}
	if stringValue(node["type"]) != args.Mode {
		return "target_type_mismatch", "目标节点类型与生成模式不匹配"
	}
	scope, err := mediaversions.ScopeForNode(repo, userID, canvasID, args.NodeID)
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
