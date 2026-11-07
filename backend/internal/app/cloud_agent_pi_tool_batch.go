package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type PiToolBatchRequest struct {
	TaskID string           `json:"taskId"`
	Calls  []cloudAgentCall `json:"calls"`
}

type PiToolReceipt struct {
	Suspended   bool            `json:"suspended,omitempty"`
	OperationID string          `json:"operationId,omitempty"`
	CallID      string          `json:"callId"`
	Pending     bool            `json:"pending"`
	Result      json.RawMessage `json:"result,omitempty"`
	IsError     bool            `json:"isError,omitempty"`
	// Terminated 表示本轮已进入终态。Result 非空时仍是已持久化的真实回执，
	// Worker 应先消费它再停止；终态查询不会重新执行工具。
	Terminated bool `json:"terminated,omitempty"`
}

func (s *Service) PiToolBatch(userID, runID, owner string, batch PiToolBatchRequest) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return nil
		}
		return err
	}
	if cloudAgentRunTerminal(run.Status) {
		return nil
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if state.SkillRuntimeMode == cloudAgentSkillRuntimeNative {
		for _, call := range batch.Calls {
			if call.Function.Name == "read" {
				return kernel.Forbidden("native Skill read does not use a canvas tool batch")
			}
		}
	}
	batch.TaskID = strings.TrimSpace(batch.TaskID)
	if batch.TaskID == "" {
		return BadAuthRequest("Pi 工具调用批次缺少模型步骤 ID")
	}
	if state.PiToolBatchTaskID == batch.TaskID && piSameCalls(state.Calls, batch.Calls) {
		return nil
	}
	// Rolling upgrades can encounter a batch admitted by the old worker before
	// PiToolBatchTaskID was added. Its source is unambiguous while LastStepTaskID
	// still names that same model result; adopt it as an idempotent replay.
	if state.PiToolBatchTaskID == "" && state.LastStepTaskID == batch.TaskID && len(state.Calls) > 0 && piSameCalls(state.Calls, batch.Calls) {
		state.PiToolBatchTaskID = batch.TaskID
		return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			return cloudAgentSave(current, &state)
		})
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || run.Status != "running" {
		return kernel.Forbidden("Agent 尚有未完成的模型或工具步骤")
	}
	text, err := s.piAdmitToolBatchEnvelope(userID, &state, batch)
	if err != nil {
		return err
	}
	installPiToolBatch(runID, &state, batch, text)
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	})
}

// piAdmitToolBatchEnvelope checks the batch against the live call limits and
// the billed model step's own tool calls, and returns that step's text.
func (s *Service) piAdmitToolBatchEnvelope(userID string, state *cloudAgentRuntime, batch PiToolBatchRequest) (string, error) {
	limits, err := s.cloudAgentStepLimits()
	if err != nil {
		return "", err
	}
	state.CallLimits = limits.CallLimits
	if err := validatePiToolBatchEnvelope(batch.Calls); err != nil || len(batch.Calls) == 0 {
		return "", BadAuthRequest("Pi 工具调用批次无效")
	}
	if len(batch.Calls) > state.callLimits().ToolCallsPerStep {
		return "", BadAuthRequest(fmt.Sprintf("单步工具调用数量 %d 超过后台配置上限 %d", len(batch.Calls), state.callLimits().ToolCallsPerStep))
	}
	// The worker can only submit calls that the billed model step actually emitted.
	if state.LastStepTaskID != batch.TaskID {
		return "", kernel.Forbidden("Pi 工具批次与模型步骤不匹配")
	}
	modelTask, err := s.repo.TaskForUser(userID, batch.TaskID)
	if err != nil {
		return "", err
	}
	if modelTask.Status != model.TaskStatusSucceeded {
		return "", kernel.Forbidden("Pi 模型步骤未成功")
	}
	var modelOutput struct {
		Text           string           `json:"text"`
		ToolCalls      []cloudAgentCall `json:"toolCalls"`
		Legacy         []cloudAgentCall `json:"tool_calls"`
		StopReasonKind string           `json:"stopReasonKind"`
	}
	if err := json.Unmarshal([]byte(modelTask.ResultJSON), &modelOutput); err != nil {
		return "", err
	}
	if state.Execution != nil {
		return piAdmitGenericToolBatchEnvelope(state, batch, modelOutput.Text, modelOutput.ToolCalls, modelOutput.Legacy, modelOutput.StopReasonKind)
	}
	stopKind := strings.TrimSpace(modelOutput.StopReasonKind)
	if stopKind == "" {
		stopKind = cloudAgentStopKindUnknown
	}
	if cloudAgentStepStopDisposition(state, stopKind) != cloudAgentStepDispositionAccept {
		return "", kernel.Forbidden("模型终止原因不允许执行工具")
	}
	expected := modelOutput.ToolCalls
	if len(expected) == 0 {
		expected = modelOutput.Legacy
	}
	if len(expected) > state.callLimits().ToolCallsPerStep {
		return "", BadAuthRequest(fmt.Sprintf("模型回复包含 %d 个工具调用，超过后台配置上限 %d", len(expected), state.callLimits().ToolCallsPerStep))
	}
	if state.SkillRuntimeMode == cloudAgentSkillRuntimeNative {
		canvasCalls := make([]cloudAgentCall, 0, len(expected))
		for _, call := range expected {
			if call.Function.Name != "read" {
				canvasCalls = append(canvasCalls, call)
			}
		}
		expected = canvasCalls
	}
	if !piSameCalls(expected, batch.Calls) {
		return "", kernel.Forbidden("Pi 工具批次与模型结果不一致")
	}
	return modelOutput.Text, nil
}

// installPiToolBatch records image observations from the model text and makes
// the admitted calls the run's current tool batch.
func installPiToolBatch(runID string, state *cloudAgentRuntime, batch PiToolBatchRequest, text string) {
	recordedObservations := state.cloudAgentRecordImageObservations(text, len(batch.Calls) > 0)
	state.cloudAgentSyncVisualAnchor()
	if recordedObservations > 0 {
		state.event(runID, "context_transition", map[string]any{
			"kind": "image_observation", "reason": "vision_ledger",
			"text":         fmt.Sprintf("视觉账本：记下 %d 张图片的观察", recordedObservations),
			"observations": recordedObservations, "pendingImages": len(state.PendingImageObservations),
		})
	}
	state.Calls = batch.Calls
	state.PiToolBatchTaskID = batch.TaskID
	state.PiToolBatchAssistantEntryID = ""
	if state.PiAssistantCheckpointTaskID == batch.TaskID {
		state.PiToolBatchAssistantEntryID = state.PiAssistantCheckpointEntryID
	}
	state.CallIndex = 0
	state.CallAdmissions = cloudAgentPreflightBatch(state, batch.Calls)
	state.CanvasBatchHashes = nil
	state.StepSnapshotHash = cloudAgentCaptureStepSnapshotHash(batch.Calls)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": batch.Calls})
}

func piSameCalls(left, right []cloudAgentCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Function.Name != right[index].Function.Name {
			return false
		}
		if !piSameToolArguments(left[index].Function.Arguments, right[index].Function.Arguments) {
			return false
		}
	}
	return true
}

func (s *Service) PiToolAdvance(userID, runID, owner, taskID, callID string) (*PiToolReceipt, error) {
	run, state, err := s.piToolAdvanceRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || state.PiToolBatchTaskID != taskID {
		return nil, kernel.Forbidden("Pi 工具调用不属于当前模型步骤")
	}
	if !piToolBatchContainsCall(state.Calls, callID) {
		return nil, kernel.Forbidden("Pi 工具调用顺序或标识无效")
	}
	if receipt := piToolReceipt(&state, taskID, callID); receipt != nil {
		receipt.Terminated = cloudAgentRunTerminal(run.Status)
		return receipt, nil
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	if state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != callID {
		return nil, kernel.Forbidden("Pi 工具调用顺序或标识无效")
	}
	if run.Status == "waiting_approval" && state.Approval != nil && state.Approval.Decision == "" {
		return &PiToolReceipt{CallID: callID, Pending: true}, nil
	}
	var advanceErr error
	if cloudAgentLegacyBlockingMedia(&state) {
		advanceErr = s.executeCloudAgentMediaCall(run, &state, state.Calls[state.CallIndex])
	} else {
		advanceErr = s.executeCloudAgentToolCall(run, &state)
	}
	if advanceErr != nil {
		return nil, advanceErr
	}
	return s.piToolAdvanceResult(userID, runID, taskID, callID)
}

// piToolAdvanceRun loads the leased run (or its terminal row after the lease was
// cleared), settles finished generations and decodes the runtime state.
func (s *Service) piToolAdvanceRun(userID, runID, owner string) (*model.CloudAgentExecution, cloudAgentRuntime, error) {
	if err := s.reconcileDynamicSubagents(context.Background(), userID, runID); err != nil {
		return nil, cloudAgentRuntime{}, err
	}
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		// 终态转换会清除 session 租约。Pi worker 可能正好在终结后轮询
		// 工具回执；对已终结的本用户 Pi run 返回终止信号，避免旧租约错误
		// 让 Node 将整轮判为协议失败。非终态仍必须通过租约 fencing。
		terminalRun, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, cloudAgentRuntime{}, err
		}
		run = terminalRun
	}
	if settled, err := s.settleCloudAgentGenerationsAndReload(run); err != nil {
		return nil, cloudAgentRuntime{}, err
	} else if settled != nil {
		run = settled
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, cloudAgentRuntime{}, err
	}
	return run, state, nil
}

// piToolAdvanceResult rereads the run after the call executed and returns its
// receipt, a terminal signal, or a pending (possibly suspended) result.
func (s *Service) piToolAdvanceResult(userID, runID, taskID, callID string) (*PiToolReceipt, error) {
	latest, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, err
	}
	if cloudAgentRunTerminal(latest.Status) {
		s.publishCloudAgentIdle(userID, runID, latest.CanvasID)
	}
	updated, err := cloudAgentDecode(latest)
	if err != nil {
		return nil, err
	}
	if receipt := piToolReceipt(&updated, taskID, callID); receipt != nil {
		receipt.Terminated = cloudAgentRunTerminal(latest.Status)
		return receipt, nil
	}
	if cloudAgentRunTerminal(latest.Status) {
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	if latest.WaitKind == "subagents" || latest.WaitKind == cloudAgentGenerationsWaitKind {
		return &PiToolReceipt{CallID: callID, Pending: true, Suspended: true}, nil
	}
	return &PiToolReceipt{CallID: callID, Pending: true}, nil
}

func piToolReceipt(state *cloudAgentRuntime, taskID, callID string) *PiToolReceipt {
	if state.PiToolBatchTaskID != taskID {
		return nil
	}
	batchIndex := piToolAssistantBatchIndex(state.Canonical.Messages, callID)
	if batchIndex < 0 {
		return nil
	}
	for index := batchIndex + 1; index < len(state.Canonical.Messages); index++ {
		message := state.Canonical.Messages[index]
		if stringField(message, "role") != "tool" || stringField(message, "tool_call_id") != callID {
			continue
		}
		receipt := &PiToolReceipt{CallID: callID, Result: json.RawMessage(stringField(message, "content"))}
		for eventIndex := len(state.Events) - 1; eventIndex >= 0; eventIndex-- {
			event := state.Events[eventIndex]
			eventTaskID := stringField(event.Payload, "taskId")
			if stringField(event.Payload, "callId") == callID && (eventTaskID == "" || eventTaskID == taskID) {
				receipt.IsError = event.Type == "tool_failed"
				break
			}
		}
		return receipt
	}
	return nil
}

func piToolBatchContainsCall(calls []cloudAgentCall, callID string) bool {
	for _, call := range calls {
		if call.ID == callID {
			return true
		}
	}
	return false
}

func piToolAssistantBatchIndex(messages []map[string]any, callID string) int {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if stringField(message, "role") != "assistant" {
			continue
		}
		encoded, err := json.Marshal(message["tool_calls"])
		if err != nil {
			continue
		}
		var calls []cloudAgentCall
		if json.Unmarshal(encoded, &calls) != nil {
			continue
		}
		if piToolBatchContainsCall(calls, callID) {
			return index
		}
	}
	return -1
}
