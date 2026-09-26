package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const piAgentLeaseDuration = 45 * time.Second

// PiAgentToolSpec is the transport-neutral snapshot of an eligible tool. The
// backend still checks each invocation against the run's permission contract.
type PiAgentToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Category    string         `json:"category,omitempty"`
	Allowed     bool           `json:"allowed"`
}

type PiAgentSnapshot struct {
	RunID                string                `json:"runId"`
	UserID               string                `json:"userId"`
	Revision             int64                 `json:"revision"`
	Status               string                `json:"status"`
	Request              CloudAgentRequest     `json:"request"`
	Canonical            canonicalAgentRequest `json:"canonical"`
	ActiveTask           string                `json:"activeTaskId,omitempty"`
	LastTaskID           string                `json:"lastTaskId,omitempty"`
	NoToolTaskID         string                `json:"noToolTaskId,omitempty"`
	NoToolNudge          string                `json:"noToolNudge,omitempty"`
	PreviousStepTemplate string                `json:"previousStepTemplate"`
	Tools                []PiAgentToolSpec     `json:"tools"`
	PiMessages           []json.RawMessage     `json:"piMessages"`
	Opened               []string              `json:"openedCategories"`
}

type PiModelStepRequest struct {
	Canonical canonicalAgentRequest `json:"canonical"`
}

type PiModelStepView struct {
	TaskID    string          `json:"taskId"`
	Status    string          `json:"status"`
	TextDraft string          `json:"textDraft,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type PiToolBatchRequest struct {
	Calls []cloudAgentCall `json:"calls"`
}

type PiMessageCheckpoint struct {
	Sequence int             `json:"sequence"`
	Message  json.RawMessage `json:"message"`
	TaskID   string          `json:"taskId,omitempty"`
}

// PiCheckpointMessage commits an assistant message and its model-task acknowledgement
// in one database transaction. A retry with the same sequence and body is a no-op.
func (s *Service) PiCheckpointMessage(userID, runID, owner string, input PiMessageCheckpoint) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	if input.Sequence < 1 || input.Sequence > 1000 || len(input.Message) == 0 || len(input.Message) > 1<<20 || !json.Valid(input.Message) {
		return BadAuthRequest("Pi 消息检查点无效")
	}
	var message struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(input.Message, &message) != nil || (message.Role != "user" && message.Role != "assistant" && message.Role != "toolResult" && message.Role != "system") {
		return BadAuthRequest("Pi 消息类型无效")
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		count := 0
		for _, record := range current.Transcript {
			if record.Kind != cloudAgentMessageKindPi {
				continue
			}
			count++
			if record.Sequence == input.Sequence {
				var oldValue, newValue any
				_ = json.Unmarshal([]byte(record.MessageJSON), &oldValue)
				_ = json.Unmarshal(input.Message, &newValue)
				if !reflect.DeepEqual(oldValue, newValue) {
					return kernel.Forbidden("Pi 消息检查点冲突")
				}
				return nil
			}
		}
		if input.Sequence != count+1 {
			return kernel.Forbidden("Pi 消息检查点序号不连续")
		}
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		if input.TaskID != "" {
			if message.Role != "assistant" || state.ActiveTaskID != input.TaskID {
				return kernel.Forbidden("Pi 模型任务与消息不匹配")
			}
			task, err := repo.TaskForUser(userID, input.TaskID)
			if err != nil {
				return err
			}
			if task.Status != model.TaskStatusSucceeded {
				return BadAuthRequest("模型任务尚未成功")
			}
			state.ActiveTaskID = ""
		}
		if err := cloudAgentSave(current, &state); err != nil {
			return err
		}
		current.Transcript = append(current.Transcript, model.CloudAgentMessageRecord{RunID: runID, UserID: userID, Kind: cloudAgentMessageKindPi, Sequence: input.Sequence, MessageJSON: string(input.Message)})
		current.MessageCount = len(current.Transcript)
		return nil
	})
}

type PiToolReceipt struct {
	CallID  string          `json:"callId"`
	Pending bool            `json:"pending"`
	Result  json.RawMessage `json:"result,omitempty"`
	IsError bool            `json:"isError,omitempty"`
}

type PiTurnDecision struct {
	Status string `json:"status"`
	Nudge  string `json:"nudge,omitempty"`
}

func (s *Service) PiNoToolTurn(userID, runID, owner, taskID string) (*PiTurnDecision, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if run.Status == "completed" {
		return &PiTurnDecision{Status: "completed"}, nil
	}
	if state.PiNoToolTaskID == taskID {
		status := run.Status
		if status == "running" {
			status = "continue"
		}
		return &PiTurnDecision{Status: status, Nudge: state.PiNoToolNudge}, nil
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || state.LastStepTaskID != taskID {
		return nil, kernel.Forbidden("Pi 收尾步骤无效")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusSucceeded {
		return nil, BadAuthRequest("模型步骤尚未成功")
	}
	var result struct {
		Text           string           `json:"text"`
		ToolCalls      []cloudAgentCall `json:"toolCalls"`
		StopReasonKind string           `json:"stopReasonKind"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		return nil, err
	}
	if len(result.ToolCalls) != 0 {
		return nil, kernel.Forbidden("Pi 收尾步骤包含工具调用")
	}
	decision := &PiTurnDecision{}
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text})
		state.PiNoToolTaskID = taskID
		completion := cloudAgentEvaluateCompletion(&state)
		completion = cloudAgentBlockTruncatedCompletion(completion, result.StopReasonKind)
		state.event(runID, "assistant_message", map[string]any{"messageId": taskID, "text": result.Text, "final": completion.Final})
		state.cloudAgentRecordImageObservations(result.Text, false)
		state.cloudAgentSyncVisualAnchor()
		if completion.Final {
			current.Status = "completed"
			decision.Status = "completed"
			return cloudAgentSave(current, &state)
		}
		if cloudAgentStepBudgetExhausted(&state) {
			completion.Attempt = state.CompletionNudgeAttempt
			decision.Status = "failed"
			return cloudAgentFailBlockedCompletion(current, &state, runID, completion)
		}
		attempt, exhausted := cloudAgentNoteCompletionBlocked(&state, completion.Fingerprint)
		if exhausted {
			completion.Attempt = attempt
			decision.Status = "failed"
			return cloudAgentFailBlockedCompletion(current, &state, runID, completion)
		}
		completion.Attempt = attempt
		cloudAgentCompletionBlockedNudge(runID, &state, completion)
		last := state.Canonical.Messages[len(state.Canonical.Messages)-1]
		decision.Status = "continue"
		decision.Nudge = stringField(last, "content")
		state.PiNoToolNudge = decision.Nudge
		return cloudAgentSave(current, &state)
	})
	return decision, err
}

func (s *Service) ClaimPiAgent(owner string) (*PiAgentSnapshot, error) {
	if owner == "" {
		return nil, BadAuthRequest("Pi worker ID is required")
	}
	run, err := s.repo.ClaimPiAgent(owner, time.Now().Add(piAgentLeaseDuration))
	if err != nil || run == nil {
		return nil, err
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) PiAgentSnapshot(userID, runID, owner string) (*PiAgentSnapshot, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) RenewPiAgentLease(userID, runID, owner string) error {
	ok, err := s.repo.RenewPiAgentLease(userID, runID, owner, time.Now().Add(piAgentLeaseDuration))
	if err != nil {
		return err
	}
	if !ok {
		return kernel.Forbidden("Pi Agent 运行租约已失效")
	}
	return nil
}

func (s *Service) PiModelStep(userID, runID, owner string, request PiModelStepRequest) (*PiModelStepView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	state.StepLimits, err = s.cloudAgentStepLimits()
	if err != nil {
		return nil, err
	}
	if state.ActiveTaskID != "" {
		return s.PiModelStepView(userID, runID, owner, state.ActiveTaskID)
	}
	if run.Status != "running" && run.Status != "queued" {
		return nil, kernel.Forbidden("Agent 当前状态不允许继续请求模型")
	}
	if len(request.Canonical.Messages) == 0 || len(request.Canonical.Messages) > 500 || len(request.Canonical.SystemPrompt) > 128<<10 {
		return nil, BadAuthRequest("Pi 模型上下文无效")
	}
	allowed := map[string]bool{}
	for _, tool := range cloudAgentVisibleToolsForCategories(state.Canonical.Tools, cloudAgentActivatedCategories(&state), nil, state.ToolScope) {
		function, _ := tool["function"].(map[string]any)
		allowed[stringField(function, "name")] = true
	}
	for _, tool := range request.Canonical.Tools {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if !allowed[name] || !cloudAgentToolAllowed(state.Request, name) {
			return nil, kernel.Forbidden("Pi 模型请求包含未披露工具")
		}
	}
	request.Canonical.PromptCacheKey = state.Canonical.PromptCacheKey
	state.DisclosureVersion = cloudAgentToolDisclosureVersion
	state.AdvertisedToolNames = cloudAgentToolNames(request.Canonical.Tools)
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	if remaining <= 0 {
		return nil, BadAuthRequest("Agent 累计预算已耗尽")
	}
	input := map[string]any{
		"mode": "text", "prompt": state.Request.Prompt,
		"agentRequests": map[string]any{"canonical": request.Canonical},
		"config":        map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)},
		"textOptions":   map[string]any{"stream": true, "thinking": cloudAgentReasoningEnabled(state.Policy.ReasoningMode), "maxOutputTokens": cloudAgentStepOutputBudget(state.StepLimits, false)},
	}
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentStepOperation, Prompt: state.Request.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	if err := s.enqueueCloudAgentTask(run, &state, req, nil); err != nil {
		return nil, err
	}
	latest, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, err
	}
	if latest.ActiveTaskID == "" {
		return nil, fmt.Errorf("Pi model task was not persisted")
	}
	return s.PiModelStepView(userID, runID, owner, latest.ActiveTaskID)
}

func (s *Service) PiModelStepView(userID, runID, owner, taskID string) (*PiModelStepView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if state.ActiveTaskID != taskID {
		return nil, kernel.Forbidden("模型任务不属于当前 Pi 步骤")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return nil, err
	}
	view := &PiModelStepView{TaskID: task.ID, Status: string(task.Status), TextDraft: task.TextDraft}
	if cloudAgentTaskTerminal(task.Status) {
		view.Result = json.RawMessage(task.ResultJSON)
		view.Error = task.Error
	}
	return view, nil
}

func (s *Service) PiFailModelStep(userID, runID, owner, taskID string) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if state.ActiveTaskID != taskID {
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return err
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning || task.Status == model.TaskStatusSucceeded {
		return BadAuthRequest("模型任务没有失败")
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = truncateRunes(task.Error, 1000)
		state.event(runID, "run_failed", map[string]any{"text": "模型任务失败", "reason": "model_step_failed", "taskId": taskID})
		return cloudAgentSave(current, &state)
	})
}

func (s *Service) PiModelStepAck(userID, runID, owner, taskID string) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if state.ActiveTaskID == "" {
		return nil
	}
	if state.ActiveTaskID != taskID {
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return err
	}
	if !cloudAgentTaskTerminal(task.Status) {
		return BadAuthRequest("模型任务尚未结束")
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.ActiveTaskID = ""
		return cloudAgentSave(current, &state)
	})
}

func (s *Service) PiToolBatch(userID, runID, owner string, batch PiToolBatchRequest) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if len(state.Calls) > 0 && piSameCalls(state.Calls, batch.Calls) {
		return nil
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || run.Status != "running" {
		return kernel.Forbidden("Agent 尚有未完成的模型或工具步骤")
	}
	if err := validateCloudAgentCalls(batch.Calls); err != nil || len(batch.Calls) == 0 || len(batch.Calls) > 16 {
		return BadAuthRequest("Pi 工具调用批次无效")
	}
	// The worker can only submit calls that the billed model step actually emitted.
	modelTask, err := s.repo.TaskForUser(userID, state.LastStepTaskID)
	if err != nil {
		return err
	}
	if modelTask.Status != model.TaskStatusSucceeded {
		return kernel.Forbidden("Pi 模型步骤未成功")
	}
	var modelOutput struct {
		ToolCalls []cloudAgentCall `json:"toolCalls"`
		Legacy    []cloudAgentCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(modelTask.ResultJSON), &modelOutput); err != nil {
		return err
	}
	expected := modelOutput.ToolCalls
	if len(expected) == 0 {
		expected = modelOutput.Legacy
	}
	if !piSameCalls(expected, batch.Calls) {
		return kernel.Forbidden("Pi 工具批次与模型结果不一致")
	}
	state.Calls = batch.Calls
	state.CallIndex = 0
	state.CallAdmissions = cloudAgentPreflightBatch(&state, batch.Calls)
	state.CanvasBatchHashes = nil
	state.StepSnapshotHash = cloudAgentCaptureStepSnapshotHash(batch.Calls)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": batch.Calls})
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	})
}

func piSameCalls(left, right []cloudAgentCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Function.Name != right[index].Function.Name {
			return false
		}
		var leftArgs, rightArgs any
		if json.Unmarshal([]byte(left[index].Function.Arguments), &leftArgs) != nil || json.Unmarshal([]byte(right[index].Function.Arguments), &rightArgs) != nil || !reflect.DeepEqual(leftArgs, rightArgs) {
			return false
		}
	}
	return true
}

func (s *Service) PiToolAdvance(userID, runID, owner, callID string) (*PiToolReceipt, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if receipt := piToolReceipt(&state, callID); receipt != nil {
		return receipt, nil
	}
	if state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != callID {
		return nil, kernel.Forbidden("Pi 工具调用顺序或标识无效")
	}
	if run.Status == "waiting_approval" && state.Approval != nil && state.Approval.Decision == "" {
		return &PiToolReceipt{CallID: callID, Pending: true}, nil
	}
	var advanceErr error
	if state.MediaTaskID != "" {
		advanceErr = s.advanceCloudAgentMedia(run, &state, state.Calls[state.CallIndex])
	} else {
		advanceErr = s.advanceCloudAgentTool(run, &state)
	}
	if advanceErr != nil {
		return nil, advanceErr
	}
	latest, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, err
	}
	updated, err := cloudAgentDecode(latest)
	if err != nil {
		return nil, err
	}
	if receipt := piToolReceipt(&updated, callID); receipt != nil {
		return receipt, nil
	}
	return &PiToolReceipt{CallID: callID, Pending: true}, nil
}

func piToolReceipt(state *cloudAgentRuntime, callID string) *PiToolReceipt {
	for index := len(state.Canonical.Messages) - 1; index >= 0; index-- {
		message := state.Canonical.Messages[index]
		if stringField(message, "role") != "tool" || stringField(message, "tool_call_id") != callID {
			continue
		}
		receipt := &PiToolReceipt{CallID: callID, Result: json.RawMessage(stringField(message, "content"))}
		for eventIndex := len(state.Events) - 1; eventIndex >= 0; eventIndex-- {
			event := state.Events[eventIndex]
			if stringField(event.Payload, "callId") == callID {
				receipt.IsError = event.Type == "tool_failed"
				break
			}
		}
		return receipt
	}
	return nil
}

func (s *Service) piAgentLeasedRun(userID, runID, owner string) (*model.CloudAgentExecution, error) {
	run, err := s.repo.CloudAgent(userID, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	if err != nil {
		return nil, err
	}
	if run.Engine != "pi" || run.LeaseOwner != owner || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(time.Now()) {
		return nil, kernel.Forbidden("Pi Agent 运行租约无效")
	}
	return run, nil
}

func (s *Service) piAgentSnapshot(run *model.CloudAgentExecution) (*PiAgentSnapshot, error) {
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	tools := make([]PiAgentToolSpec, 0, len(state.Canonical.Tools))
	for _, item := range state.Canonical.Tools {
		function, _ := item["function"].(map[string]any)
		name := stringField(function, "name")
		parameters, _ := function["parameters"].(map[string]any)
		if name == "" || parameters == nil {
			continue
		}
		tools = append(tools, PiAgentToolSpec{
			Name: name, Description: stringField(function, "description"), Parameters: parameters,
			Category: cloudAgentToolCategory(name), Allowed: cloudAgentToolAllowed(state.Request, name),
		})
	}
	return &PiAgentSnapshot{
		RunID: run.ID, UserID: run.UserID, Revision: run.Revision, Status: run.Status,
		Request: state.Request, Canonical: state.Canonical, ActiveTask: state.ActiveTaskID, LastTaskID: state.LastStepTaskID, NoToolTaskID: state.PiNoToolTaskID, NoToolNudge: state.PiNoToolNudge, PreviousStepTemplate: cloudAgentToolText("previous_step_calls"), Tools: tools,
		Opened: state.ActivatedToolCategories, PiMessages: piAgentMessages(run),
	}, nil
}

func piAgentMessages(run *model.CloudAgentExecution) []json.RawMessage {
	messages := make([]json.RawMessage, 0)
	for _, record := range run.Transcript {
		if record.Kind == cloudAgentMessageKindPi {
			messages = append(messages, json.RawMessage(record.MessageJSON))
		}
	}
	return messages
}
