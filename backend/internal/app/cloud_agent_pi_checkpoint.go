package app

import (
	"encoding/json"
	"reflect"
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type PiMessageCheckpoint struct {
	Sequence        int               `json:"sequence"`
	Message         json.RawMessage   `json:"message"`
	TaskID          string            `json:"taskId,omitempty"`
	InterjectionIDs []string          `json:"interjectionIds,omitempty"`
	SessionRevision int64             `json:"sessionRevision,omitempty"`
	ActiveLeafID    string            `json:"activeLeafId,omitempty"`
	SessionEntries  []json.RawMessage `json:"sessionEntries,omitempty"`
}

type PiCheckpointMessageOutcome struct {
	Saved           bool   `json:"saved"`
	SessionRevision int64  `json:"sessionRevision,omitempty"`
	Terminated      bool   `json:"terminated,omitempty"`
	Status          string `json:"status,omitempty"`
}

// piCheckpointMessage is the part of a Pi message checkpoint that Go validates
// and projects into run events.
type piCheckpointMessage struct {
	Role         string `json:"role"`
	Text         string `json:"text"`
	Content      any    `json:"content"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

// piCheckpointCommit carries one validated checkpoint into its CAS transaction
// and returns the session revision the transaction produced.
type piCheckpointCommit struct {
	userID, runID          string
	input                  PiMessageCheckpoint
	message                piCheckpointMessage
	sessionEntries         []model.CloudAgentPiEntry
	assistantText          string
	reasoningText          string
	publishAssistantText   bool
	updatedSessionRevision int64
}

// PiCheckpointMessageOutcome makes an in-flight checkpoint harmless when a
// user cancellation or another terminal transition wins the race. It never
// writes a new message after terminal state has been committed.
func (s *Service) PiCheckpointMessageOutcome(userID, runID, owner string, input PiMessageCheckpoint) (PiCheckpointMessageOutcome, error) {
	activeRun, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		if run, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return PiCheckpointMessageOutcome{Terminated: true, Status: run.Status}, nil
		}
		return PiCheckpointMessageOutcome{}, err
	}
	if cloudAgentRunTerminal(activeRun.Status) {
		return PiCheckpointMessageOutcome{Terminated: true, Status: activeRun.Status}, nil
	}
	revision, err := s.PiCheckpointMessageResult(userID, runID, owner, input)
	if err != nil {
		if run, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return PiCheckpointMessageOutcome{Terminated: true, Status: run.Status}, nil
		}
		return PiCheckpointMessageOutcome{}, err
	}
	return PiCheckpointMessageOutcome{Saved: true, SessionRevision: revision}, nil
}

// PiCheckpointMessage commits an assistant message and its model-task acknowledgement
// in one database transaction. A retry with the same sequence and body is a no-op.
func (s *Service) PiCheckpointMessage(userID, runID, owner string, input PiMessageCheckpoint) error {
	_, err := s.PiCheckpointMessageResult(userID, runID, owner, input)
	return err
}

func (s *Service) PiCheckpointMessageResult(userID, runID, owner string, input PiMessageCheckpoint) (int64, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return 0, err
	}
	message, err := piValidateMessageCheckpoint(input)
	if err != nil {
		return 0, err
	}
	sessionEntries, err := s.piCheckpointSessionEntries(userID, runID, run, input)
	if err != nil {
		return 0, err
	}
	// 旧 SSE 合同在模型每次返回正文时发 assistant_message（含"正文 + 工具调用"同时出现的
	// 过程说明，见 cloud_agent_runtime.go 的 result.Text != "" 分支）。Pi 路径原先只在
	// PiNoToolTurn（无工具调用的收尾步骤）发这条事件，模型"边做边说"时前端会丢掉过程消息。
	// 这里与检查点同事务补发，payload 与旧路径保持一致。
	assistantText := piAssistantText(message.Content)
	if message.Text != "" {
		assistantText = message.Text
	}
	commit := &piCheckpointCommit{
		userID: userID, runID: runID, input: input, message: message, sessionEntries: sessionEntries,
		assistantText: assistantText,
		// 推理正文同样有既有 SSE 事件（cloud_agent_runtime.go 的 reasoning_message）。
		reasoningText: piReasoningText(message.Content),
		// Billed steps without tools are published by PiNoToolTurn after its completion
		// gate. Publishing them here first duplicates candidates and leaks rejected or
		// truncated text before that gate has classified the provider stop reason.
		publishAssistantText: input.TaskID == "",
	}
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		return s.piCommitMessageCheckpoint(repo, current, commit)
	})
	if err != nil {
		return 0, err
	}
	return commit.updatedSessionRevision, nil
}

// piValidateMessageCheckpoint checks the checkpoint envelope before any read.
func piValidateMessageCheckpoint(input PiMessageCheckpoint) (piCheckpointMessage, error) {
	var message piCheckpointMessage
	if input.Sequence < 1 || input.Sequence > 1000 || len(input.Message) == 0 || len(input.Message) > 1<<20 || !json.Valid(input.Message) || len(input.SessionEntries) > 128 {
		return message, BadAuthRequest("Pi 消息检查点无效")
	}
	var sessionEntryBytes int
	for _, entry := range input.SessionEntries {
		sessionEntryBytes += len(entry)
		if len(entry) == 0 || len(entry) > 1<<20 || !json.Valid(entry) || sessionEntryBytes > 512<<10 {
			return message, BadAuthRequest("Pi 会话检查点无效")
		}
	}
	if input.SessionRevision < 0 || input.SessionRevision > 1<<62 {
		return message, BadAuthRequest("Pi 会话版本无效")
	}
	if json.Unmarshal(input.Message, &message) != nil || (message.Role != "user" && message.Role != "assistant" && message.Role != "toolResult" && message.Role != "system") {
		return message, BadAuthRequest("Pi 消息类型无效")
	}
	return message, piValidateCheckpointInterjections(input, message)
}

func piValidateCheckpointInterjections(input PiMessageCheckpoint, message piCheckpointMessage) error {
	if len(input.InterjectionIDs) > cloudAgentInterjectionIDMemory || (len(input.InterjectionIDs) > 0 && message.Role != "user") {
		return BadAuthRequest("Pi 插话检查点无效")
	}
	seenInterjectionIDs := make(map[string]struct{}, len(input.InterjectionIDs))
	for _, id := range input.InterjectionIDs {
		if strings.TrimSpace(id) == "" {
			return BadAuthRequest("Pi 插话检查点无效")
		}
		if _, duplicate := seenInterjectionIDs[id]; duplicate {
			return BadAuthRequest("Pi 插话检查点无效")
		}
		seenInterjectionIDs[id] = struct{}{}
	}
	return nil
}

// piCheckpointSessionEntries resolves the Pi session and decodes the v3 entries
// that commit together with the message checkpoint.
func (s *Service) piCheckpointSessionEntries(userID, runID string, run *model.CloudAgentExecution, input PiMessageCheckpoint) ([]model.CloudAgentPiEntry, error) {
	var piSessionID string
	if len(input.SessionEntries) > 0 {
		session, _, err := s.repo.CloudAgentPiSession(userID, firstNonEmpty(run.ConversationID, run.ID))
		if err != nil {
			return nil, err
		}
		piSessionID = session.ID
	}
	var sessionEntries []model.CloudAgentPiEntry
	for _, raw := range input.SessionEntries {
		var identity struct {
			ID       string  `json:"id"`
			ParentID *string `json:"parentId"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil || strings.TrimSpace(identity.ID) == "" {
			return nil, BadAuthRequest("Pi 会话条目缺少 ID")
		}
		parentID := ""
		if identity.ParentID != nil {
			parentID = *identity.ParentID
		}
		sessionEntries = append(sessionEntries, model.CloudAgentPiEntry{
			SessionID: piSessionID, EntryID: identity.ID, ParentID: parentID,
			UserID: userID, RunID: runID, EntryJSON: string(raw),
		})
	}
	return sessionEntries, nil
}

// piCommitMessageCheckpoint runs inside MutateCloudAgent. A retry of a stored
// sequence is only re-verified; a new sequence updates state and appends.
func (s *Service) piCommitMessageCheckpoint(repo *repository.Repository, current *model.CloudAgentExecution, commit *piCheckpointCommit) error {
	count := 0
	for _, record := range current.Transcript {
		if record.Kind != cloudAgentMessageKindPi {
			continue
		}
		count++
		if record.Sequence == commit.input.Sequence {
			return piReplayMessageCheckpoint(repo, current, record, commit)
		}
	}
	if commit.input.Sequence != count+1 {
		return kernel.Forbidden("Pi 消息检查点序号不连续")
	}
	state, err := cloudAgentDecode(current)
	if err != nil {
		return err
	}
	if err := s.piApplyCheckpointToState(repo, current, &state, commit); err != nil {
		return err
	}
	if len(commit.input.InterjectionIDs) > 0 {
		if err := cloudAgentCommitPiInterjections(commit.runID, &state, commit.input.InterjectionIDs, commit.message.Content); err != nil {
			return err
		}
	}
	if err := cloudAgentSave(current, &state); err != nil {
		return err
	}
	current.Transcript = append(current.Transcript, model.CloudAgentMessageRecord{RunID: commit.runID, UserID: commit.userID, Kind: cloudAgentMessageKindPi, Sequence: commit.input.Sequence, MessageJSON: string(commit.input.Message)})
	current.MessageCount = len(current.Transcript)
	if err := piPublishCheckpointMessages(current, &state, commit); err != nil {
		return err
	}
	return piAppendCheckpointSession(repo, current, commit)
}

// piReplayMessageCheckpoint accepts a retry of a stored sequence only when its
// body and interjections match, then re-applies the session entries.
func piReplayMessageCheckpoint(repo *repository.Repository, current *model.CloudAgentExecution, record model.CloudAgentMessageRecord, commit *piCheckpointCommit) error {
	var oldValue, newValue any
	_ = json.Unmarshal([]byte(record.MessageJSON), &oldValue)
	_ = json.Unmarshal(commit.input.Message, &newValue)
	if !reflect.DeepEqual(oldValue, newValue) {
		return kernel.Forbidden("Pi 消息检查点冲突")
	}
	if len(commit.input.InterjectionIDs) > 0 {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		for _, id := range commit.input.InterjectionIDs {
			if !containsString(state.InterjectionIDs, id) {
				return kernel.Forbidden("Pi 插话检查点冲突")
			}
		}
	}
	return piAppendCheckpointSession(repo, current, commit)
}

// piAppendCheckpointSession advances the Pi session tree in the same transaction.
func piAppendCheckpointSession(repo *repository.Repository, current *model.CloudAgentExecution, commit *piCheckpointCommit) error {
	input := commit.input
	if input.SessionRevision > 0 || len(input.SessionEntries) > 0 || input.ActiveLeafID != "" {
		revision, err := repo.AppendCloudAgentPiSessionEntries(
			commit.userID, current.ConversationID, commit.runID, input.SessionRevision, input.ActiveLeafID, commit.sessionEntries,
		)
		if err != nil {
			return err
		}
		commit.updatedSessionRevision = revision
	}
	return nil
}

// piApplyCheckpointToState updates run state for a new checkpoint before its
// transcript row is appended.
func (s *Service) piApplyCheckpointToState(repo *repository.Repository, current *model.CloudAgentExecution, state *cloudAgentRuntime, commit *piCheckpointCommit) error {
	input, message := commit.input, commit.message
	if input.TaskID == "" && message.Role == "assistant" && message.StopReason == "error" && message.ErrorMessage == "context_length_exceeded" {
		failedTask, err := repo.TaskForUser(commit.userID, state.PiModelFailureTaskID)
		if err != nil || !piModelTaskContextOverflow(failedTask) || state.ActiveTaskID != failedTask.ID {
			return kernel.Forbidden("Pi overflow recovery checkpoint has no matching failed task")
		}
		state.ActiveTaskID, state.PiModelStepFingerprint = "", ""
	}
	if message.Role == "toolResult" {
		piSettleLocallyRejectedTool(current, state, input.Message)
	}
	if state.SkillRuntimeMode == cloudAgentSkillRuntimeNative && message.Role == "toolResult" {
		if err := s.piCheckpointNativeSkillRead(repo, current, state, commit); err != nil {
			return err
		}
	}
	if input.TaskID != "" {
		return s.piCheckpointModelTaskOutput(repo, state, commit)
	}
	return nil
}

// piCheckpointNativeSkillRead validates a native Skill read result and records
// its success or failure event.
func (s *Service) piCheckpointNativeSkillRead(repo *repository.Repository, current *model.CloudAgentExecution, state *cloudAgentRuntime, commit *piCheckpointCommit) error {
	var result struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
	}
	if json.Unmarshal(commit.input.Message, &result) != nil {
		return BadAuthRequest("Pi Skill read result is invalid")
	}
	if result.ToolName != "read" {
		return nil
	}
	if err := s.validatePiNativeRawRead(repo, commit.userID, state, current.Transcript, result.ToolCallID, result.IsError); err != nil {
		return err
	}
	skill, path, err := nativeReadCheckpointCall(state, current.Transcript, result.ToolCallID, !result.IsError)
	if err != nil {
		return err
	}
	kind := "native_skill_read"
	payload := map[string]any{"toolName": "read"}
	if result.IsError {
		kind = "native_skill_read_failed"
	}
	if skill != nil {
		payload["skillId"], payload["nativeName"], payload["skillName"] = skill.ID, skill.NativeName, skill.Name
		payload["path"], payload["version"] = path, firstNonEmpty(skill.VersionLabel, skill.Version)
	}
	state.event(commit.runID, kind, payload)
	return nil
}

// piCheckpointModelTaskOutput binds the assistant checkpoint to its succeeded
// model task and decides whether the process text is published.
func (s *Service) piCheckpointModelTaskOutput(repo *repository.Repository, state *cloudAgentRuntime, commit *piCheckpointCommit) error {
	input := commit.input
	if commit.message.Role != "assistant" || state.ActiveTaskID != input.TaskID {
		return kernel.Forbidden("Pi 模型任务与消息不匹配")
	}
	task, err := repo.TaskForUser(commit.userID, input.TaskID)
	if err != nil {
		return err
	}
	if task.Status != model.TaskStatusSucceeded {
		return BadAuthRequest("模型任务尚未成功")
	}
	var output struct {
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
	if err := json.Unmarshal([]byte(task.ResultJSON), &output); err != nil {
		return BadAuthRequest("Pi 模型任务结果无效")
	}
	stopKind := strings.TrimSpace(output.StopReasonKind)
	if stopKind == "" {
		stopKind = normalizeCloudAgentStopReason(output.StopReason)
	}
	disposition := cloudAgentStepStopDisposition(state, stopKind)
	commit.publishAssistantText = (len(output.ToolCalls) > 0 || len(output.LegacyCalls) > 0) &&
		disposition == cloudAgentStepDispositionAccept
	if disposition != cloudAgentStepDispositionRetry {
		cloudAgentRestoreEscalationSwitches(state)
	}
	if commit.publishAssistantText {
		state.event(commit.runID, "model_step_stop", cloudAgentStopReasonPayload(state.Step, input.TaskID,
			output.StopReason, stopKind, disposition, cloudAgentStepStopFacts{
				TerminalEventSeen: output.TerminalEventSeen, StopReasonPresent: output.StopReasonPresent, StreamDoneSeen: output.StreamDoneSeen,
			}, len(output.Text), len(output.Reasoning), max(len(output.ToolCalls), len(output.LegacyCalls))))
	}
	// Context pressure for the next Pi step must use the upstream-reported
	// usage of this completed step when available. Read through this same
	// transaction so the usage anchor and assistant checkpoint commit together.
	s.recordCloudAgentTokenAnchorWithRepository(repo, commit.userID, state)
	state.PiAssistantCheckpointTaskID = input.TaskID
	state.PiAssistantCheckpointEntryID = piCheckpointAssistantEntryID(input)
	state.ActiveTaskID = ""
	return nil
}

// piPublishCheckpointMessages 让过程说明（final=false）与消息检查点同事务提交：前端订阅事件时不会出现
// "有消息行但没有事件"或反之的中间态。
func piPublishCheckpointMessages(current *model.CloudAgentExecution, state *cloudAgentRuntime, commit *piCheckpointCommit) error {
	if commit.message.Role != "assistant" {
		return nil
	}
	messageID := taskIDForPiMessage(commit.input, commit.runID)
	changed := false
	if strings.TrimSpace(commit.reasoningText) != "" {
		state.event(commit.runID, "reasoning_message", map[string]any{
			"messageId": messageID + ":reasoning", "text": truncateRunes(commit.reasoningText, 8000),
		})
		changed = true
	}
	if commit.publishAssistantText && strings.TrimSpace(commit.assistantText) != "" {
		state.event(commit.runID, "assistant_message", map[string]any{
			"messageId": messageID, "text": commit.assistantText, "final": false,
		})
		changed = true
	}
	if changed {
		return cloudAgentSave(current, state)
	}
	return nil
}

// Pi validates arguments before invoking the Canvas tool. Its local error must
// close an already admitted call, but cannot stand in for a successful side
// effect. Only Go's persisted preflight rejection authorizes this settlement.
func piSettleLocallyRejectedTool(run *model.CloudAgentExecution, state *cloudAgentRuntime, raw json.RawMessage) {
	var result struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.IsError || state.PiToolBatchTaskID == "" || state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return
	}
	call := state.Calls[state.CallIndex]
	if call.ID != result.ToolCallID || call.Function.Name != result.ToolName {
		return
	}
	admission, ok := cloudAgentAdmissionFor(state, state.CallIndex)
	if !ok || admission.Allowed || admission.CallID != call.ID {
		return
	}
	cloudAgentRecordToolResult(run, state, call, nil, cloudAgentAdmissionError(admission))
}

func nativeReadCheckpointCall(state *cloudAgentRuntime, transcript []model.CloudAgentMessageRecord, callID string, requireFile bool) (*cloudAgentSkill, string, error) {
	if callID == "" {
		return nil, "", kernel.Forbidden("native Skill read call is missing")
	}
	for index := len(transcript) - 1; index >= 0; index-- {
		record := transcript[index]
		if record.Kind != cloudAgentMessageKindPi {
			continue
		}
		var message struct {
			Role       string `json:"role"`
			ToolCallID string `json:"toolCallId"`
			Content    []struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(record.MessageJSON), &message) != nil {
			continue
		}
		if message.Role == "toolResult" && message.ToolCallID == callID {
			return nil, "", kernel.Forbidden("native Skill read result was already checkpointed")
		}
		if message.Role != "assistant" {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "toolCall" || block.ID != callID || block.Name != "read" {
				continue
			}
			var arguments struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(block.Arguments, &arguments)
			// 系统提示给的是相对路径 skills/<name>/SKILL.md；补一个前导斜杠，
			// 让相对与绝对路径都能按同一标记定位到技能目录。
			path := "/" + strings.TrimLeft(strings.ReplaceAll(arguments.Path, "\\", "/"), "/")
			for skillIndex := range state.Skills {
				skill := &state.Skills[skillIndex]
				marker := "/skills/" + skill.NativeName + "/"
				position := strings.LastIndex(path, marker)
				if position < 0 {
					continue
				}
				relativePath, err := normalizeNativeSkillReadPath(path[position+len(marker):])
				if err != nil {
					if !requireFile {
						return nil, "", nil
					}
					return nil, "", err
				}
				for _, file := range skill.NativeFiles {
					if file.Path == relativePath && file.Text {
						return skill, relativePath, nil
					}
				}
			}
			// Rejected reads still need a paired durable result. Never publish an
			// unvalidated model path (or its error text) in the public event.
			if !requireFile {
				return nil, "", nil
			}
		}
		return nil, "", kernel.Forbidden("native Skill read call is not paired with an enabled file")
	}
	return nil, "", kernel.Forbidden("native Skill read result has no assistant call")
}
