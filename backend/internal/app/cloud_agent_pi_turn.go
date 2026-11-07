package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type PiTurnDecision struct {
	Status string `json:"status"`
	Nudge  string `json:"nudge,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// piNoToolTurnOutput is the part of a succeeded model task result that the
// no-tool turn gate classifies.
type piNoToolTurnOutput struct {
	Text              string           `json:"text"`
	Reasoning         string           `json:"reasoning"`
	ToolCalls         []cloudAgentCall `json:"toolCalls"`
	LegacyCalls       []cloudAgentCall `json:"tool_calls"`
	StopReasonKind    string           `json:"stopReasonKind"`
	StopReason        string           `json:"stopReason"`
	TerminalEventSeen bool             `json:"terminalEventSeen"`
	StopReasonPresent bool             `json:"stopReasonPresent"`
	StreamDoneSeen    bool             `json:"streamDoneSeen"`
}

// piNoToolTurnResult loads the succeeded model task and its normalized stop kind.
func (s *Service) piNoToolTurnResult(userID, taskID string) (piNoToolTurnOutput, string, error) {
	var result piNoToolTurnOutput
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return result, "", err
	}
	if task.Status != model.TaskStatusSucceeded {
		return result, "", BadAuthRequest("模型步骤尚未成功")
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		return result, "", err
	}
	stopKind := strings.TrimSpace(result.StopReasonKind)
	if stopKind == "" {
		stopKind = normalizeCloudAgentStopReason(result.StopReason)
	}
	return result, stopKind, nil
}

func (s *Service) PiNoToolTurn(userID, runID, owner, taskID string) (*PiTurnDecision, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, err
		}
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiTurnDecision{Status: run.Status}, nil
	}
	if state.Execution != nil {
		return s.piGenericNoToolTurn(userID, run, state, taskID, owner)
	}
	limits, err := s.cloudAgentStepLimits()
	if err != nil {
		return nil, err
	}
	state.CallLimits = limits.CallLimits
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
	result, stopKind, err := s.piNoToolTurnResult(userID, taskID)
	if err != nil {
		return nil, err
	}
	callCount := max(len(result.ToolCalls), len(result.LegacyCalls))
	if callCount != 0 && cloudAgentStepStopDisposition(&state, stopKind) == cloudAgentStepDispositionAccept {
		return nil, kernel.Forbidden("Pi 收尾步骤包含工具调用")
	}
	decision := &PiTurnDecision{}
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		disposition := cloudAgentStepStopDisposition(&state, stopKind)
		state.event(runID, "model_step_stop", cloudAgentStopReasonPayload(state.Step, taskID,
			result.StopReason, stopKind, disposition, cloudAgentStepStopFacts{
				TerminalEventSeen: result.TerminalEventSeen, StopReasonPresent: result.StopReasonPresent, StreamDoneSeen: result.StreamDoneSeen,
			}, len(result.Text), len(result.Reasoning), callCount))
		if disposition == cloudAgentStepDispositionRetry {
			state.PiNoToolTaskID = taskID
			state.PiNoToolNudge = "上一模型步骤因输出长度限制被截断；本次已关闭思考并使用可用输出预算重试。请完整处理尚未完成的用户请求，不要重复本轮已经成功的画布操作。"
			cloudAgentEscalateTruncatedStep(runID, &state)
			decision.Status, decision.Nudge = "continue", state.PiNoToolNudge
			return cloudAgentSave(current, &state)
		}
		if disposition == cloudAgentStepDispositionFail {
			message := cloudAgentStepStopFailureMessage(stopKind)
			current.Status = "failed"
			current.FailureMessage = truncateRunes(message, 1000)
			state.PiNoToolTaskID = taskID
			state.PiNoToolNudge = ""
			cloudAgentDropInterjections(runID, "本轮已结束："+truncateRunes(message, 120), &state)
			state.event(runID, "run_failed", map[string]any{
				"text": message, "reason": cloudAgentStepStopFailureReason(stopKind),
				"stopReason": result.StopReason, "stopReasonKind": stopKind,
			})
			decision.Status = "failed"
			return cloudAgentSave(current, &state)
		}
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text})
		state.PiNoToolTaskID = taskID
		if err := refreshActiveSubagents(repo, current, &state); err != nil {
			return err
		}
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
	if err == nil && cloudAgentRunTerminal(decision.Status) {
		s.publishCloudAgentIdle(userID, runID, state.Request.CanvasID)
	}
	return decision, err
}

func (s *Service) piGenericNoToolTurn(userID string, run *model.CloudAgentExecution, state cloudAgentRuntime, taskID, owner string) (*PiTurnDecision, error) {
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || state.LastStepTaskID != taskID {
		return nil, kernel.Forbidden("通用 Pi 收尾步骤无效")
	}
	result, _, err := s.piNoToolTurnResult(userID, taskID)
	if err != nil {
		return nil, err
	}
	if len(result.ToolCalls) > 0 || len(result.LegacyCalls) > 0 {
		return nil, kernel.Forbidden("通用 Pi 收尾步骤包含工具调用")
	}
	decision := &PiTurnDecision{Status: "completed"}
	err = s.repo.MutateCloudAgent(userID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		latest, decodeErr := cloudAgentDecode(current)
		if decodeErr != nil {
			return decodeErr
		}
		return s.completeGenericPiNoToolTurn(repo, current, &latest, taskID, result, owner)
	})
	if err == nil {
		s.publishCloudAgentIdle(userID, run.ID, run.CanvasID)
	}
	return decision, err
}

func piModelStepFingerprint(request PiModelStepRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return cloudAgentTextDigest(string(encoded)), nil
}

func (s *Service) PiFailModelStep(userID, runID, owner, taskID string) error {
	_, err := s.PiFailModelStepResult(userID, runID, owner, taskID)
	return err
}

func (s *Service) PiFailModelStepResult(userID, runID, owner, taskID string) (*PiTurnDecision, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, err
		}
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	// 任务查询提到最前面：下面两条守卫（"成功任务不得被报失败"与终态幂等）都要用它。
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return nil, err
	}
	// 协议不变量，与运行状态无关：已成功的任务永远不能被上报为失败。
	// 必须排在终态幂等守卫**之前**，否则幂等会把真正的协议违规一起吞掉。
	if task.Status == model.TaskStatusSucceeded {
		return nil, kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if state.PiModelFailureTaskID == taskID {
		status := run.Status
		if !cloudAgentRunTerminal(status) {
			status = "continue"
		}
		decision := &PiTurnDecision{Status: status, Nudge: state.PiModelFailureNudge}
		if status == "continue" && piModelTaskContextOverflow(task) {
			decision.Reason = "context_overflow"
		}
		return decision, nil
	}
	if run.Status == "failed" {
		// 重投：worker 可能在"已经记录失败、但没收到响应"之间崩溃。只有同一失败
		// 任务的重投才幂等成功；否则 400 会被 Node 当成协议错误并整轮退出。
		if state.ActiveTaskID == "" || (state.ActiveTaskID == taskID && samePiFailure(run.FailureMessage, task.Error)) {
			return &PiTurnDecision{Status: run.Status}, nil
		}
		return nil, kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if cloudAgentRunTerminal(run.Status) {
		// 运行已经以 completed / cancelled / rejected 终结，而 worker 的 /fail 仍在途
		// （它先收到终态快照，这个请求已经在路上）。这是竞态的**正常结果**，不是错误。
		//
		// 必须返回 nil：bridge 会把确定性 4xx 归类为 FatalWorkerError 并整轮退出，
		// 对一次已经正确终结的运行回错误，等于把正常收尾变成 worker 报错。
		// 修复前这里没有守卫，会把 cancelled / rejected / completed 无条件改写成 failed。
		return &PiTurnDecision{Status: run.Status}, nil
	}
	if state.ActiveTaskID != taskID {
		return nil, kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return nil, BadAuthRequest("模型任务没有失败")
	}
	if cloudAgentStepTimedOut(task) {
		return &PiTurnDecision{Status: "failed"}, s.failCloudAgentStepTimeout(run, &state, task)
	}
	decision := &PiTurnDecision{}
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		cloudAgentRestoreEscalationSwitches(&state)
		nudge, reason := "", ""
		switch {
		case piModelTaskContextOverflow(task):
			reason = "context_overflow"
		case cloudAgentTruncatedToolArguments(task):
			nudge = piAssistantText(cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextTruncatedArguments})["content"])
			reason = "truncated_arguments_retried"
		case cloudAgentEmptyModelOutput(task) && state.EmptyOutputNudged < cloudAgentMaxEmptyOutputNudges:
			state.EmptyOutputNudged++
			nudge = piAssistantText(cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextEmptyOutput})["content"])
			reason = "empty_output_retried"
		case cloudAgentEmptyModelOutput(task) && state.EmptyOutputEscalated < cloudAgentMaxEmptyOutputEscalations:
			state.EmptyOutputEscalated++
			state.ForceThinkingOff, state.BoostStepOutputBudget = true, true
			nudge, reason = "上游连续返回空内容；已关闭思考并放大输出预算。请继续处理原请求并返回有效正文或工具调用。", "empty_output_escalated"
		case cloudAgentStreamStalled(task) && state.StreamStallRetried < cloudAgentMaxStreamStallRetries:
			// 中转在生成长工具参数时断流：本步没有任何工具调用落地，告诉模型把大写入拆小后重发这一步。
			state.StreamStallRetried++
			nudge = piAssistantText(cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextStreamStalled})["content"])
			reason = "stream_stalled_retried"
		}
		state.PiModelFailureTaskID, state.PiModelFailureNudge = taskID, nudge
		if nudge != "" || reason == "context_overflow" {
			// Keep an overflow task resumable until Pi's sanitized error checkpoint
			// is durable. A crash between /fail and that checkpoint must replay it.
			if reason != "context_overflow" {
				state.ActiveTaskID, state.PiModelStepFingerprint = "", ""
			}
			state.Calls, state.CallIndex = nil, 0
			state.event(runID, "model_failure_recovered", map[string]any{"text": nudge, "reason": reason, "taskId": taskID})
			decision.Status, decision.Nudge, decision.Reason = "continue", nudge, reason
			return cloudAgentSave(current, &state)
		}
		current.Status = "failed"
		text, reason := cloudAgentModelFailure(task)
		current.FailureMessage = truncateRunes(text, 1000)
		cloudAgentDropInterjections(runID, "本轮已结束："+truncateRunes(text, 120), &state)
		state.event(runID, "run_failed", map[string]any{"text": text, "reason": reason, "taskId": taskID})
		decision.Status = "failed"
		return cloudAgentSave(current, &state)
	})
	return decision, err
}

// Return a stable internal classification; raw provider errors must not become
// Pi messages or public lifecycle payloads. Pi owns the compact-and-retry limit.
func piModelTaskContextOverflow(task *model.Task) bool {
	if task.Status != model.TaskStatusFailed {
		return false
	}
	text := strings.ToLower(task.Error)
	if strings.Contains(text, "rate limit") || strings.Contains(text, "tokens per minute") {
		return false
	}
	for _, marker := range []string{"context_length_exceeded", "model_context_window_exceeded", "maximum context length", "context window exceeded", "context window exceeds limit", "prompt is too long", "input is too long", "exceeds the context window", "range of input length should be", "configured context size"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (s *Service) PiModelStepAck(userID, runID, owner, taskID string) error {
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
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		s.recordCloudAgentTokenAnchorWithRepository(repo, userID, &state)
		state.ActiveTaskID = ""
		return cloudAgentSave(current, &state)
	})
}

// samePiFailure 只用于失败上报的幂等重投判定：运行上记录的失败文案就是该步骤
// 任务错误截断后的结果，两者相同说明这次上报已经落库。
func samePiFailure(recorded, taskError string) bool {
	return truncateRunes(taskError, 1000) == recorded
}
