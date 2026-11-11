package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"yingce/backend/internal/canvas/layout"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// cloudAgentMediaBatchControlArgs 是 media_batch_control 的参数：只能在已批准的明细和额度内操作。
type cloudAgentMediaBatchControlArgs struct {
	Action string `json:"action"`
	Items  []struct {
		Index  *int   `json:"index"`
		NodeID string `json:"nodeId"`
		Prompt string `json:"prompt"`
	} `json:"items"`
}

func (s *Service) advanceCloudAgentMediaBatchControl(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	var args cloudAgentMediaBatchControlArgs
	record := func(result any, toolErr error) error {
		s.storageMu.Lock()
		defer s.storageMu.Unlock()
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			cloudAgentRecordToolResult(current, state, call, result, toolErr)
			return cloudAgentSave(current, state)
		})
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return record(nil, cloudAgentJSONArgumentError(err))
	}
	batch := state.MediaBatch
	if batch == nil || batch.Phase == cloudAgentMediaBatchPhaseApproval {
		return record(nil, cloudAgentFieldError("action", "invalid_value", "本轮没有已批准的批次；请先调用 generate_media_batch 申请批准"))
	}
	if batch.active() {
		return record(nil, cloudAgentFieldError("action", "invalid_value", "批次仍在执行，等它结束后再操作"))
	}
	switch args.Action {
	case "status":
		return record(cloudAgentMediaBatchResult(batch), nil)
	case "cancel":
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			changed := []int{}
			for index := range batch.Items {
				if batch.Items[index].Status == cloudAgentMediaBatchItemPending {
					batch.Items[index].Status = cloudAgentMediaBatchItemCancelled
					changed = append(changed, index)
				}
			}
			batch.Phase = cloudAgentMediaBatchPhaseDone
			batch.MaxRetries = 0
			if err := repo.ReleaseCloudAgentResourceLeases(run.UserID, batch.ID); err != nil {
				return err
			}
			s.emitCloudAgentMediaBatchProgress(run.ID, state, changed...)
			cloudAgentRecordToolResult(current, state, call, cloudAgentMediaBatchResult(batch), nil)
			return cloudAgentSave(current, state)
		})
	case "continue":
		if batch.Phase != cloudAgentMediaBatchPhaseCanaryDone {
			return record(nil, cloudAgentFieldError("action", "invalid_value", "只有试跑结束后才需要放行；批次已全部执行"))
		}
	case "retry":
		if len(args.Items) == 0 || len(args.Items) > len(batch.Items) {
			return record(nil, cloudAgentFieldError("items", "required", "retry 需要列出要重跑的条目（index 或 nodeId）"))
		}
	default:
		return record(nil, cloudAgentFieldError("action", "invalid_value", "action 只能是 continue、retry、cancel 或 status"))
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return err
	}
	s.storageMu.Lock()
	err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		changed := []int{}
		if args.Action == "retry" {
			for position, request := range args.Items {
				index := -1
				if request.Index != nil {
					index = *request.Index
				} else {
					for candidate, item := range batch.Items {
						if item.NodeID == request.NodeID || cloudAgentContainsString(item.PreviousNodeIDs, request.NodeID) {
							index = candidate
						}
					}
				}
				if index < 0 || index >= len(batch.Items) {
					return cloudAgentFieldError(fmt.Sprintf("items[%d]", position), "not_found", "条目不在本批里")
				}
				item := &batch.Items[index]
				if item.Status != cloudAgentMediaBatchItemFailed && item.Status != cloudAgentMediaBatchItemSucceeded {
					return cloudAgentFieldError(fmt.Sprintf("items[%d]", position), "invalid_value", "只有已结束的条目可以重跑")
				}
				if item.Attempts >= batch.MaxRetries {
					return cloudAgentFieldError(fmt.Sprintf("items[%d]", position), "item_count", "该条目已用完批准的重试次数；需要再跑请重新申请批次")
				}
				if err := cloudAgentPrepareMediaBatchRetry(repo, run.UserID, state.Request.CanvasID, run.ID, item, strings.TrimSpace(request.Prompt), policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
					return err
				}
				changed = append(changed, index)
			}
		}
		batch.CallID = call.ID
		if args.Action == "retry" && batch.Phase == cloudAgentMediaBatchPhaseCanaryDone {
			batch.Phase = cloudAgentMediaBatchPhaseCanary
		} else {
			batch.Phase = cloudAgentMediaBatchPhaseRunning
		}
		s.emitCloudAgentMediaBatchProgress(run.ID, state, changed...)
		return cloudAgentSave(current, state)
	})
	s.storageMu.Unlock()
	if err != nil {
		var argumentErr *cloudAgentArgumentError
		if errors.As(err, &argumentErr) {
			latest, readErr := s.repo.CloudAgent(run.UserID, run.ID)
			if readErr != nil {
				return err
			}
			fresh, decodeErr := cloudAgentDecode(latest)
			if decodeErr != nil {
				return err
			}
			run, *state = latest, fresh
			return record(nil, err)
		}
		return err
	}
	s.startCloudAgentMediaBatchRunner(run.UserID, run.ID)
	return nil
}

// cloudAgentPrepareMediaBatchRetry 为重跑准备目标节点：失败节点没有产物，重置为本轮草稿再用；
// 已成功的节点保留原结果，在旁边新建同款占位节点承接重跑，不覆盖用户已有的成品。
func cloudAgentPrepareMediaBatchRetry(repo *repository.Repository, userID, canvasID, runID string, item *cloudAgentMediaBatchItem, prompt string, policy RuntimePolicySetting, recorder cloudAgentMutationRecorder) error {
	var a cloudAgentMediaArgs
	if err := json.Unmarshal([]byte(item.Arguments), &a); err != nil {
		return BadAuthRequest("批次条目参数无效")
	}
	if len([]rune(prompt)) > 16000 {
		return cloudAgentFieldError("items.prompt", "length_exceeded", "重跑提示词超过 16000 字符")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return err
	}
	before := canvas.PayloadJSON
	beforeHash := cloudAgentCanvasHash(doc)
	nodes := creationMaps(doc["nodes"])
	var original map[string]any
	for _, node := range nodes {
		if stringValue(node["id"]) == item.NodeID {
			original = node
		}
	}
	if original == nil {
		return cloudAgentFieldError("items.nodeId", "not_found", "原节点已被删除，无法重跑")
	}
	meta, _ := original["metadata"].(map[string]any)
	if meta["locked"] == true {
		return cloudAgentFieldError("items.nodeId", "target_locked", "原节点已锁定，无法重跑")
	}
	if item.Status == cloudAgentMediaBatchItemFailed && stringValue(meta["storageKey"]) == "" {
		for _, key := range []string{"taskId", "taskStatus", "taskStage", "taskProgress", "generationTaskId", "error", "errorDetails"} {
			delete(meta, key)
		}
		meta["status"], meta["agentDraftRunId"] = "idle", runID
		original["metadata"] = meta
	} else {
		width, _ := original["width"].(float64)
		height, _ := original["height"].(float64)
		if width <= 0 || height <= 0 {
			width, height = cloudAgentAddedNodeSize(stringValue(original["type"]))
		}
		position, _ := original["position"].(map[string]any)
		x, _ := position["x"].(float64)
		y, _ := position["y"].(float64)
		lane, _ := cloudAgentLayoutLane(stringValue(original["type"]))
		slot := layout.FreeSlot(width, height, lane, cloudAgentLayoutNodes(doc), &layout.Position{X: x + width + 24, Y: y}, 24)
		siblingID := cloudAgentID(userID, fmt.Sprintf("%s:retry:%s:%d", runID, item.NodeID, item.Attempts+1))
		spec, _ := meta["generationSpec"]
		sibling := creationAddedNode(CreationCanvasOp{Type: "add_node", ID: siblingID, NodeType: stringValue(original["type"]), Title: truncateRunes(stringValue(original["title"]), 200) + fmt.Sprintf(" · 重试%d", item.Attempts+1), X: &slot.X, Y: &slot.Y, Width: &width, Height: &height, Metadata: map[string]any{"status": "idle", "content": "", "agentDraftRunId": runID, "generationSpec": spec}})
		nodes = append(nodes, sibling)
		edges := creationMaps(doc["connections"])
		edges = cloudAgentRetryConnections(edges, doc, item.NodeID, siblingID)
		doc["connections"] = edges
		item.PreviousNodeIDs = append(item.PreviousNodeIDs, item.NodeID)
		item.NodeID = siblingID
		a.NodeID = siblingID
		a.Title = stringValue(sibling["title"])
	}
	doc["nodes"] = nodes
	if err := saveCloudAgentDocument(repo, canvas, doc, policy); err != nil {
		return err
	}
	if recorder != nil {
		if err := recorder(repo, cloudAgentMutationInput{RunID: runID, UserID: userID, CanvasID: canvasID, StepID: item.NodeID, Operation: "generate_media_draft", BeforeSnapshotHash: beforeHash, AfterSnapshotHash: cloudAgentCanvasHash(doc), BeforeJSON: before}); err != nil {
			return err
		}
	}
	if prompt != "" {
		a.Prompt = prompt
		item.PromptChanged = true
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	item.Arguments = string(raw)
	item.Attempts++
	item.Status, item.Error = cloudAgentMediaBatchItemPending, ""
	return nil
}

// validateCloudAgentMediaBatch 是检查点不变量：激活的批次必须挂在当前调用上，且与单媒体任务互斥。
func validateCloudAgentMediaBatch(state *cloudAgentRuntime) error {
	batch := state.MediaBatch
	if batch == nil {
		return nil
	}
	if validateCloudAgentID(batch.ID, "批次 ID", 200) != nil || len(batch.Items) == 0 || len(batch.Items) > cloudAgentMediaBatchMaxItems || batch.Concurrency < 1 || batch.MaxRetries < 0 || batch.MaxRetries > cloudAgentMediaBatchMaxRetries {
		return errors.New("Agent runtime media batch is invalid")
	}
	switch batch.Phase {
	case cloudAgentMediaBatchPhaseApproval, cloudAgentMediaBatchPhaseCanary, cloudAgentMediaBatchPhaseCanaryDone, cloudAgentMediaBatchPhaseRunning, cloudAgentMediaBatchPhaseDone:
	default:
		return errors.New("Agent runtime media batch phase is invalid")
	}
	for _, item := range batch.Items {
		switch item.Status {
		case cloudAgentMediaBatchItemPending, cloudAgentMediaBatchItemSubmitted, cloudAgentMediaBatchItemSucceeded, cloudAgentMediaBatchItemFailed, cloudAgentMediaBatchItemCancelled:
		default:
			return errors.New("Agent runtime media batch item is invalid")
		}
		if item.Status == cloudAgentMediaBatchItemSubmitted && (item.taskID() == "" || !cloudAgentContainsString(state.TaskIDs, item.taskID())) {
			return errors.New("Agent runtime media batch task is not in task history")
		}
	}
	if batch.active() {
		if state.MediaTaskID != "" || state.ActiveTaskID != "" {
			return errors.New("Agent runtime has multiple active tasks")
		}
		if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != batch.CallID {
			return errors.New("Agent runtime media batch is not attached to current call")
		}
	}
	return nil
}

func cloudAgentRetryConnections(edges []map[string]any, doc map[string]any, nodeID, siblingID string) []map[string]any {
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["toNodeId"]) == nodeID && stringValue(edge["toHandleId"]) == "" && stringValue(edge["fromHandleId"]) == "" {
			edges = append(edges, map[string]any{"id": "agent-" + newID(), "fromNodeId": edge["fromNodeId"], "toNodeId": siblingID})
		}
	}

	return edges
}
