package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const cloudAgentContextCompactionOperation = "cloud_agent_context_compaction"

func cloudAgentContextShouldCompact(state *cloudAgentRuntime) (bool, int, int) {
	if state == nil || state.ContextCompaction != nil {
		return false, 0, 0
	}
	raw, err := json.Marshal(state.Canonical.Messages)
	if err != nil {
		return false, 0, 0
	}
	turnCount := cloudAgentConversationTurnCount(state.Canonical.Messages)
	historyMessages := len(state.TextHistory) + 2
	return agentcontext.ShouldCompact(historyMessages, len(raw)), len(raw), turnCount
}

func cloudAgentConversationTurnCount(messages []map[string]any) int {
	count := 0
	for _, message := range messages {
		if stringField(message, "role") != "user" {
			continue
		}
		content := stringField(message, "content")
		if strings.Contains(content, "<agent-context-checkpoint>") {
			if checkpoint, err := agentcontext.ParseFrame(content); err == nil && checkpoint.CompactedTurnCount > count {
				count = checkpoint.CompactedTurnCount
			}
			continue
		}
		count++
	}
	return count
}

func cloudAgentContextFacts(events []CloudAgentEvent) []map[string]any {
	start := max(0, len(events)-200)
	facts := make([]map[string]any, 0, len(events)-start)
	for _, event := range events[start:] {
		switch event.Type {
		case "tool_completed", "tool_failed", "generation_task_created", "approval_decided", "run_failed", "canvas_changed":
		default:
			continue
		}
		if stringValue(event.Payload["toolName"]) == "skills_load" {
			continue
		}
		fact := map[string]any{"event": event.Type, "seq": event.Seq}
		for _, key := range []string{"toolName", "callId", "nodeId", "nodeIds", "referenceNodeIds", "taskId", "title", "summary", "status", "decision", "phase", "taskSubmitted", "reason", "operation"} {
			if value, ok := event.Payload[key]; ok {
				fact[key] = value
			}
		}
		if result, ok := event.Payload["result"].(map[string]any); ok {
			for _, key := range []string{"nodeId", "nodeIds", "referenceNodeIds", "taskId", "title", "summary", "status", "phase", "taskSubmitted"} {
				if value, exists := result[key]; exists {
					fact[key] = value
				}
			}
		}
		facts = append(facts, fact)
	}
	return facts
}

func cloudAgentJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

func cloudAgentContextCompactionPrompt(state *cloudAgentRuntime) string {
	turnCount := cloudAgentConversationTurnCount(state.Canonical.Messages)
	if state.ContextCompaction != nil && state.ContextCompaction.TurnCount > turnCount {
		turnCount = state.ContextCompaction.TurnCount
	}
	return agentcontext.BuildPrompt(agentcontext.Source{
		ConversationJSON: cloudAgentJSON(state.Canonical.Messages),
		OperationsJSON:   cloudAgentJSON(cloudAgentContextFacts(state.Events)),
		CreativeJSON:     cloudAgentJSON(state.CreativeAnchor),
		PreferencesJSON:  cloudAgentJSON(state.Profile.Layers),
		DecisionsJSON:    cloudAgentJSON(state.Decisions),
		TurnCount:        turnCount,
	})
}

func cloudAgentFallbackCheckpoint(state *cloudAgentRuntime) agentcontext.Checkpoint {
	checkpoint := agentcontext.Checkpoint{Version: agentcontext.Version}
	if state.ContextCompaction != nil {
		checkpoint.CompactedTurnCount = state.ContextCompaction.TurnCount
	}
	var history []string
	for _, message := range state.Canonical.Messages {
		role := stringField(message, "role")
		content := strings.TrimSpace(stringField(message, "content"))
		if previous, err := agentcontext.ParseFrame(content); err == nil {
			checkpoint = previous
			continue
		}
		if (role == "user" || role == "assistant") && content != "" && !strings.Contains(content, "<agent-context-checkpoint>") {
			history = append(history, role+": "+truncateRunes(content, 1800))
		}
	}
	if len(history) > 8 {
		history = history[len(history)-8:]
	}
	currentHistory := strings.Join(history, "\n")
	checkpoint.HistorySummary = truncateRunes(strings.TrimSpace(checkpoint.HistorySummary+"\n"+currentHistory), 10000)
	checkpoint.ScriptDesign = truncateRunes(strings.TrimSpace(checkpoint.ScriptDesign+"\n"+state.CreativeAnchor.UserPrompt+"\n"+currentHistory), 10000)
	checkpoint.CurrentWork = truncateRunes(state.Request.Prompt, 3000)
	checkpoint.NextStep = "继续当前工作；执行前重新读取画布和任务状态，并优先处理未完成任务。"
	checkpoint.Constraints = append(checkpoint.Constraints, state.CreativeAnchor.LockedRequirements...)
	checkpoint.Decisions = append(checkpoint.Decisions, state.CreativeAnchor.FreelyDecidable...)
	checkpoint.Constraints = append(checkpoint.Constraints, fmt.Sprintf("权限模式：%s；本轮预算上限：%.4f credits", state.Request.PermissionMode, state.Request.Budget.MaxCredits))
	for _, layer := range state.Profile.Layers {
		if content := strings.TrimSpace(layer.Content); content != "" {
			checkpoint.UserPreferences = append(checkpoint.UserPreferences, layer.Scope+": "+truncateRunes(content, 2400))
		}
	}
	keys := make([]string, 0, len(state.Decisions))
	for key := range state.Decisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		checkpoint.Decisions = append(checkpoint.Decisions, key+": "+truncateRunes(state.Decisions[key], 1200))
	}
	for _, fact := range cloudAgentContextFacts(state.Events) {
		encoded := truncateRunes(cloudAgentJSON(fact), 1200)
		checkpoint.OperationHistory = append(checkpoint.OperationHistory, encoded)
		if fact["event"] == "generation_task_created" || stringValue(fact["status"]) == "running" {
			checkpoint.PendingTasks = append(checkpoint.PendingTasks, encoded)
		}
	}
	if len(checkpoint.OperationHistory) > 24 {
		checkpoint.OperationHistory = checkpoint.OperationHistory[len(checkpoint.OperationHistory)-24:]
	}
	if len(checkpoint.PendingTasks) > 12 {
		checkpoint.PendingTasks = checkpoint.PendingTasks[len(checkpoint.PendingTasks)-12:]
	}
	return checkpoint
}

func cloudAgentBoundCheckpoint(checkpoint agentcontext.Checkpoint) agentcontext.Checkpoint {
	checkpoint.HistorySummary = truncateRunes(checkpoint.HistorySummary, 3000)
	checkpoint.ScriptDesign = truncateRunes(checkpoint.ScriptDesign, 4000)
	checkpoint.CurrentWork = truncateRunes(checkpoint.CurrentWork, 800)
	checkpoint.NextStep = truncateRunes(checkpoint.NextStep, 500)
	bound := func(values []string, maxItems, maxRunes int) []string {
		if len(values) > maxItems {
			values = values[len(values)-maxItems:]
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			result = append(result, truncateRunes(value, maxRunes))
		}
		return result
	}
	checkpoint.OperationHistory = bound(checkpoint.OperationHistory, 10, 150)
	checkpoint.PendingTasks = bound(checkpoint.PendingTasks, 6, 150)
	checkpoint.Decisions = bound(checkpoint.Decisions, 8, 150)
	checkpoint.Constraints = bound(checkpoint.Constraints, 8, 150)
	checkpoint.UserPreferences = bound(checkpoint.UserPreferences, 6, 300)
	return checkpoint
}

func cloudAgentRecentConversation(messages []map[string]any, pairs int) []providerTextMessage {
	complete := make([]providerTextMessage, 0, pairs*2)
	var pending *providerTextMessage
	for _, message := range messages {
		role, content := stringField(message, "role"), strings.TrimSpace(stringField(message, "content"))
		if (role != "user" && role != "assistant") || content == "" || strings.Contains(content, "<agent-context-checkpoint>") {
			continue
		}
		if role == "user" {
			candidate := providerTextMessage{Role: role, Content: content}
			pending = &candidate
			continue
		}
		if _, hasCalls := message["tool_calls"]; hasCalls || pending == nil {
			continue
		}
		complete = append(complete, *pending, providerTextMessage{Role: role, Content: content})
		pending = nil
	}
	start := max(0, len(complete)-pairs*2)
	return append([]providerTextMessage(nil), complete[start:]...)
}

func cloudAgentCheckpointHistory(checkpoint agentcontext.Checkpoint, recent []providerTextMessage) ([]providerTextMessage, error) {
	framed, err := agentcontext.Frame(checkpoint)
	if err != nil {
		return nil, err
	}
	history := []providerTextMessage{{Role: "user", Content: framed}, {Role: "assistant", Content: agentcontext.Acknowledgement}}
	return append(history, recent...), nil
}

func (s *Service) enqueueCloudAgentContextCompaction(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	prompt := cloudAgentContextCompactionPrompt(state)
	input := map[string]any{
		"mode": "text", "prompt": prompt,
		"config":      map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model), "systemPrompt": "只执行服务端上下文压缩合同。不要调用工具，不要生成面向用户的回复。"},
		"textOptions": map[string]any{"stream": false, "thinking": false},
	}
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentContextCompactionOperation, Prompt: prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	return s.enqueueCloudAgentTask(run, state, req, nil)
}

func (s *Service) advanceCloudAgentContextCompaction(run *model.CloudAgentExecution, state *cloudAgentRuntime, task *model.Task) error {
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return nil
	}
	checkpoint := cloudAgentFallbackCheckpoint(state)
	mode, reason := "fallback", "压缩模型任务未成功，已使用服务端保底检查点"
	if task.Status == model.TaskStatusSucceeded {
		var result struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(task.ResultJSON), &result); err == nil {
			if parsed, parseErr := agentcontext.Parse(result.Text); parseErr == nil {
				checkpoint, mode, reason = parsed, "model", ""
			} else {
				reason = "压缩模型输出不符合检查点合同，已使用服务端保底检查点"
			}
		} else {
			reason = "压缩模型结果损坏，已使用服务端保底检查点"
		}
	}
	if state.ContextCompaction != nil {
		checkpoint.CompactedTurnCount = state.ContextCompaction.TurnCount
	}
	return s.persistCloudAgentContextCheckpoint(run, state, checkpoint, mode, reason)
}

func (s *Service) completeCloudAgentContextFallback(run *model.CloudAgentExecution, state *cloudAgentRuntime, reason string) error {
	checkpoint := cloudAgentFallbackCheckpoint(state)
	if state.ContextCompaction != nil {
		checkpoint.CompactedTurnCount = state.ContextCompaction.TurnCount
	}
	return s.persistCloudAgentContextCheckpoint(run, state, checkpoint, "fallback", reason)
}

func (s *Service) persistCloudAgentContextCheckpoint(run *model.CloudAgentExecution, state *cloudAgentRuntime, checkpoint agentcontext.Checkpoint, mode, reason string) error {
	checkpoint = cloudAgentBoundCheckpoint(checkpoint)
	recent := cloudAgentRecentConversation(state.Canonical.Messages, 2)
	history, err := cloudAgentCheckpointHistory(checkpoint, recent)
	if err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.ContextCheckpoint = &checkpoint
		state.TextHistory = history
		state.HistoryIncludesCurrent = true
		state.Canonical.Messages = make([]map[string]any, 0, len(history))
		for _, message := range history {
			state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": message.Role, "content": message.Content})
		}
		state.ActiveTaskID = ""
		state.ActiveTextDraft = ""
		state.ContextCompaction = nil
		current.Status = "completed"
		payload := map[string]any{"mode": mode, "compactedTurnCount": checkpoint.CompactedTurnCount, "historyMessages": len(history)}
		if reason != "" {
			payload["reason"] = reason
		}
		state.event(run.ID, "context_compacted", payload)
		return cloudAgentSave(current, state)
	})
}
