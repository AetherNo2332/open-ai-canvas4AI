package app

import (
	"encoding/json"
	"fmt"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// canvasPlatformSubmission 把一条画布命令提交的任务登记为平台任务。画布命令回执已按
// 画布 + 纪元 + 客户端 + 操作做幂等，命令重放不会再次进入 Commit，所以平台键直接使用任务 ID，与画布绑定一一对应。
func canvasPlatformSubmission(task model.Task) *model.PlatformSubmission {
	var input struct {
		Metadata map[string]any `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(task.InputJSON), &input)
	nodeID := stringValue(input.Metadata["nodeId"])
	business := cp.BusinessRef{Application: "canvas", ProjectID: task.ProjectID, ObjectType: "node", ObjectID: nodeID, OperationID: fmt.Sprintf("%s:%s", task.ProjectID, task.ID)}
	encoded, _ := json.Marshal(business)
	return &model.PlatformSubmission{
		UserID: task.UserID, Application: "canvas", IdempotencyKey: "canvas-task:" + task.ID, Fingerprint: task.ID,
		TaskID: task.ID, Capability: capabilityFromTaskType(task.Type), BusinessJSON: string(encoded),
		ProjectRef: task.ProjectID, ObjectType: business.ObjectType, ObjectID: nodeID, OperationID: business.OperationID,
	}
}
