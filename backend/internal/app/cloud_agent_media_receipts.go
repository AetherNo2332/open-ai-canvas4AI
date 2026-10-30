package app

import (
	"encoding/json"
	"fmt"
	"strings"

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
	operation := key.OperationID
	refresh := func() error {
		state.event(run.ID, "canvas_updated", map[string]any{"canvasId": key.CanvasID, "operation": input.Operation, "requiresRefresh": true, "taskId": taskID, "resultOperationId": operation})
		return nil
	}
	stored, err := repo.CommandReceipt(key)
	if repo.IsMissing(err) {
		return refresh()
	}
	if err != nil {
		return err
	}
	var receipt commands.Receipt
	if err = json.Unmarshal([]byte(stored.ReceiptJSON), &receipt); err != nil {
		return err
	}
	if receipt.CanvasID != key.CanvasID || receipt.DocumentEpoch != key.Epoch || receipt.ActorID != key.ActorID || receipt.ClientID != key.ClientID || receipt.OperationID != key.OperationID || receipt.Revision < 1 {
		return BadAuthRequest("任务结果回执身份不一致")
	}
	history, err := repo.ActorHistoryForCommand(key, receipt.EventSeq)
	if repo.IsMissing(err) {
		return refresh()
	}
	if err != nil {
		return err
	}
	var forward, inverse commands.ChangeSet
	if err = json.Unmarshal([]byte(history.ForwardJSON), &forward); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(history.InverseJSON), &inverse); err != nil {
		return err
	}
	// Only committed values are used. Unchanged entities are not needed for a delta.
	project := func(changes commands.ChangeSet) map[string]any {
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
	before, after := project(inverse), project(forward)
	after["updatedAt"] = history.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	if err = emitCloudAgentCanvasDocuments(run.ID, state, input, before, after, receipt.Revision); err != nil {
		return err
	}
	for index := len(state.Events) - 1; index >= 0; index-- {
		event := &state.Events[index]
		if event.Type != "canvas_updated" || event.Payload["operation"] != input.Operation {
			continue
		}
		event.Payload["resultOperationId"], event.Payload["documentEpoch"] = operation, key.Epoch
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
	return nil
}
