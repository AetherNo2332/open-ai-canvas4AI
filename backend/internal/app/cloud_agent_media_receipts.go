package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func cloudAgentMediaResultKey(repo *repository.Repository, userID, canvasID, taskID string) (model.CanvasCommandReceipt, bool, error) {
	key := model.CanvasCommandReceipt{}
	binding, err := repo.CanvasTaskBinding(taskID)
	if repo.IsMissing(err) {
		return key, false, nil
	}
	if err != nil {
		return key, false, err
	}
	if binding.OwnerID != userID || binding.CanvasID != canvasID {
		return key, false, BadAuthRequest("任务画布归属不一致")
	}
	operation := "task-result:" + taskID
	if binding.ResolutionVersion > 0 {
		operation += fmt.Sprintf(":resolution:%d", binding.ResolutionVersion)
	}
	return model.CanvasCommandReceipt{OwnerID: binding.OwnerID, CanvasID: binding.CanvasID, Epoch: binding.Epoch, ActorID: binding.ActorID, ClientID: binding.ClientID, OperationID: operation}, true, nil
}

// The command receipt and immutable history delta commit with materialization.
// Read them in the Agent checkpoint transaction, including after a worker restart.
func emitCloudAgentMediaCompletion(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, taskID, fallbackBefore string) error {
	input := cloudAgentMutationInput{UserID: run.UserID, CanvasID: state.Request.CanvasID, Operation: "generate_media_complete", BeforeJSON: fallbackBefore}
	key, bound, err := cloudAgentMediaResultKey(repo, run.UserID, state.Request.CanvasID, taskID)
	if err != nil {
		return err
	}
	if !bound {
		return emitCloudAgentCanvasChange(repo, run.ID, state, input)
	}
	refresh := func() error {
		state.event(run.ID, "canvas_updated", map[string]any{"canvasId": key.CanvasID, "operation": input.Operation, "requiresRefresh": true, "taskId": taskID, "resultOperationId": key.OperationID})
		return nil
	}
	history, err := cloudAgentLoadMediaResultHistory(repo, key)
	if err != nil {
		return err
	}
	if history == nil {
		return refresh()
	}
	// Only committed values are used. Unchanged entities are not needed for a delta.
	before, after := cloudAgentProjectChangeSet(history.inverse), cloudAgentProjectChangeSet(history.forward)
	after["updatedAt"] = history.createdAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	if err = emitCloudAgentCanvasDocuments(run.ID, state, input, before, after, history.receipt.Revision); err != nil {
		return err
	}
	cloudAgentPatchMediaCompletionEvent(state, input, key, history.forward, history.inverse)
	return nil
}

// cloudAgentMediaResultHistory 是任务结果回执与对应的双向增量，全部来自已提交的命令。
type cloudAgentMediaResultHistory struct {
	receipt   commands.Receipt
	forward   commands.ChangeSet
	inverse   commands.ChangeSet
	createdAt time.Time
}

// cloudAgentLoadMediaResultHistory 读取任务结果回执与不可变历史增量；回执或历史缺失时
// 返回 (nil, nil)，由调用方退化为 requiresRefresh 通知。身份不一致与解码失败按错误返回。
func cloudAgentLoadMediaResultHistory(repo *repository.Repository, key model.CanvasCommandReceipt) (*cloudAgentMediaResultHistory, error) {
	stored, err := repo.CommandReceipt(key)
	if repo.IsMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipt commands.Receipt
	if err = json.Unmarshal([]byte(stored.ReceiptJSON), &receipt); err != nil {
		return nil, err
	}
	if receipt.CanvasID != key.CanvasID || receipt.DocumentEpoch != key.Epoch || receipt.ActorID != key.ActorID || receipt.ClientID != key.ClientID || receipt.OperationID != key.OperationID || receipt.Revision < 1 {
		return nil, BadAuthRequest("任务结果回执身份不一致")
	}
	history, err := repo.ActorHistoryForCommand(key, receipt.EventSeq)
	if repo.IsMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var forward, inverse commands.ChangeSet
	if err = json.Unmarshal([]byte(history.ForwardJSON), &forward); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(history.InverseJSON), &inverse); err != nil {
		return nil, err
	}
	return &cloudAgentMediaResultHistory{receipt: receipt, forward: forward, inverse: inverse, createdAt: history.CreatedAt}, nil
}

// cloudAgentProjectChangeSet 把命令增量投影成画布文档片段：删除项与未变更实体不进入 delta。
func cloudAgentProjectChangeSet(changes commands.ChangeSet) map[string]any {
	doc := map[string]any{"nodes": []any{}, "connections": []any{}}
	for _, change := range changes.Entities {
		if change.Delete {
			continue
		}
		collection := ""
		if strings.HasPrefix(change.Key, "node:") {
			collection = "nodes"
		}
		if strings.HasPrefix(change.Key, "connection:") {
			collection = "connections"
		}
		if collection != "" {
			doc[collection] = append(doc[collection].([]any), change.Value)
		}
	}
	for key, field := range changes.Fields {
		if !field.Delete {
			doc[key] = field.Value
		}
	}
	return doc
}

// cloudAgentPatchMediaCompletionEvent 给本步最新的 canvas_updated 事件补任务结果
// 操作 ID、文档纪元与节点/连接顺序，供前端做增量刷新。
func cloudAgentPatchMediaCompletionEvent(state *cloudAgentRuntime, input cloudAgentMutationInput, key model.CanvasCommandReceipt, forward, inverse commands.ChangeSet) {
	for index := len(state.Events) - 1; index >= 0; index-- {
		event := &state.Events[index]
		if event.Type != "canvas_updated" || event.Payload["operation"] != input.Operation {
			continue
		}
		event.Payload["resultOperationId"], event.Payload["documentEpoch"] = key.OperationID, key.Epoch
		if patch, ok := event.Payload["canvasPatch"].(map[string]any); ok {
			if forward.NodeOrder != nil {
				patch["nodeOrder"], patch["previousNodeOrder"] = forward.NodeOrder, inverse.NodeOrder
			}
			if forward.ConnectionOrder != nil {
				patch["connectionOrder"], patch["previousConnectionOrder"] = forward.ConnectionOrder, inverse.ConnectionOrder
			}
		}
		break
	}
}
