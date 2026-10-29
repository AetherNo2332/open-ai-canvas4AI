package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// errMediaVersionUnchanged 表示写回不改动画布（目标已删除、绑定已变化等）：画布写入回滚，
// 只在单独的事务里记录绑定结果并结束写回任务，不产生空的画布修订。
var errMediaVersionUnchanged = errors.New("media version writeback leaves canvas unchanged")

func mediaVersionError(err error) error {
	var version *mediaversions.Error
	if errors.As(err, &version) {
		return &commands.Error{Reason: version.Reason, Message: version.Message}
	}
	return err
}

// settleMediaVersion 把媒体版本任务的结果写进节点 metadata.mediaVersions：成功时登记并采用新版本
// （等价于浏览器 recordReplacedMediaVersion）；生成期间用户换过当前版本时只进入历史。
func settleMediaVersion(tx *repository.Repository, doc commands.Document, binding model.CanvasTaskBinding, base mediaversions.Base, task model.Task, outputs []model.CanvasTaskOutput, consumer Consumer) (commands.Document, string, error) {
	node := findNode(doc, base.NodeID)
	blocked := ""
	var row map[string]any
	if consumer.Kind == "storyboard" || consumer.Kind == "batch" {
		if parent := findNode(doc, binding.TargetNodeID); parent != nil {
			row = consumerRow(parent, consumer)
		}
		field := consumer.Field
		if consumer.Kind == "batch" {
			field = "outputNodeId"
		}
		switch {
		case row == nil:
			blocked = "row_deleted"
		case str(row["taskId"]) != task.ID:
			blocked = "binding_changed"
		case str(row[field]) != base.NodeID || consumer.OutputNodeID != base.NodeID:
			blocked = "output_binding_changed"
		}
	}
	// 投影记录的单调标记在行级原因之后：具体的行删除/改绑原因优先报告。
	if blocked == "" {
		blocked = base.Blocked
	}
	if blocked == "" && node != nil && base.OutputOwners != nil && fmt.Sprint(mediaversions.OutputOwners(objectList(doc["nodes"]), base.NodeID)) != fmt.Sprint(base.OutputOwners) {
		blocked = "output_binding_changed"
	}
	if node == nil {
		return nil, "target_deleted", errMediaVersionUnchanged
	}
	if _, err := mediaversions.ScopeForNode(tx, binding.OwnerID, binding.CanvasID, base.NodeID); err != nil {
		return nil, "target_deleted", errMediaVersionUnchanged
	}
	before, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	result := map[string]any{"id": node["id"], "type": node["type"]}
	if task.Status == model.TaskStatusSucceeded && blocked == "" {
		if len(outputs) == 0 {
			return nil, "", fmt.Errorf("media version output missing")
		}
		result = cloneNode(node)
		if err := applyOutput(result, task, outputs[0]); err != nil {
			return nil, "", err
		}
	}
	if task.Status == model.TaskStatusFailed && task.MediaRecoveryJSON != "" {
		// 媒体已生成但保存未完成：保持生成标记，恢复原任务后再写回，不允许重新付费生成。
		return doc, "media_save_pending", nil
	}
	settled, outcome := mediaversions.Finish(node, result, base, task, blocked, time.Now())
	replaceNode(doc, settled)
	claimField := consumer.Field
	if consumer.Kind == "batch" {
		claimField = "outputNodeId"
	}
	if row != nil && str(row["taskId"]) == task.ID && str(row[claimField]) == base.NodeID && blocked != "" {
		// A restored row may still carry the original pending marker. Release
		// only that marker; never apply the result into its changed ownership.
		delete(row, "taskId")
		delete(row, "taskStatus")
		delete(row, "errorDetails")
		row["status"] = "idle"
		if mediaversions.HasMedia(settled) {
			row["status"] = "success"
		}
	} else if row != nil && str(row["taskId"]) == task.ID && blocked == "" {
		row["taskStatus"] = string(task.Status)
		row["status"] = resultStatus(task.Status)
		if task.Status != model.TaskStatusSucceeded {
			row["errorDetails"] = safeError(task)
		} else {
			delete(row, "errorDetails")
		}
	}
	base.ResultVersionID, base.Adopted, base.Settled = outcome.VersionID, outcome.Adopted, true
	if after, err := json.Marshal(doc); err != nil {
		return nil, "", err
	} else if string(after) == string(before) {
		return nil, outcome.Reason, errMediaVersionUnchanged
	}
	if err := tx.UpdateCanvasTaskBindingMediaVersion(task.ID, binding.OwnerID, base.Encode()); err != nil {
		return nil, "", err
	}
	return doc, outcome.Reason, nil
}

func cloneNode(node map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range node {
		out[key] = value
	}
	m := map[string]any{}
	for key, value := range metadata(node) {
		m[key] = value
	}
	out["metadata"] = m
	if position, ok := node["position"].(map[string]any); ok {
		copied := map[string]any{}
		for key, value := range position {
			copied[key] = value
		}
		out["position"] = copied
	}
	return out
}

func replaceNode(doc commands.Document, next map[string]any) {
	switch nodes := doc["nodes"].(type) {
	case []any:
		for i, value := range nodes {
			if node, ok := value.(map[string]any); ok && str(node["id"]) == str(next["id"]) {
				nodes[i] = next
				return
			}
		}
	case []map[string]any:
		for i, node := range nodes {
			if str(node["id"]) == str(next["id"]) {
				nodes[i] = next
				return
			}
		}
	}
}
