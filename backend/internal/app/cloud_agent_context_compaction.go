package app

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const cloudAgentContextCompactionOperation = "cloud_agent_context_compaction"

const (
	// cloudAgentCompactionRatio 是触发压缩的上下文利用率：下一步预计输入 token
	// （优先上游实测锚点投影）达到"用户在该渠道模型能力里填的可用输入"的比例时，
	// 暂停步进循环、把历史压成检查点，然后继续。
	cloudAgentCompactionRatio = 0.8
	// cloudAgentMaxCompactionsPerRun 限制同一轮最多压几次：压完仍然超阈值时不能无限暂停。
	cloudAgentMaxCompactionsPerRun = 4
)

// cloudAgentContextShouldCompact 是"没配模型上下文上限"时的兜底判据。
// 条数必须数当前会话：历史实现数的是 state.TextHistory（压缩后残留的历史，实测最多 7 条），
// 于是"≥16 条"这条规则在真实会话里永远不成立——38 条消息的会话也照样不压缩。
func cloudAgentContextShouldCompact(state *cloudAgentRuntime) (bool, int, int) {
	if state == nil || state.ContextCompaction != nil {
		return false, 0, 0
	}
	raw, err := json.Marshal(state.Canonical.Messages)
	if err != nil {
		return false, 0, 0
	}
	turnCount := cloudAgentConversationTurnCount(state.Canonical.Messages)
	return agentcontext.ShouldCompact(len(state.Canonical.Messages), len(raw)), len(raw), turnCount
}

// cloudAgentPressureReading 是一次压缩判据的读数（token meter 口径）。
type cloudAgentPressureReading struct {
	ProjectedTokens   int     `json:"projectedTokens"`
	UsableInputTokens int     `json:"usableInputTokens"`
	Ratio             float64 `json:"ratio"`
	TokenSource       string  `json:"tokenSource"`
}

// cloudAgentCompactionReading 用 token meter 的口径算"离压缩还有多远"：
// 上限取用户在该渠道模型能力里填的上下文窗口减去预留输出，当前值优先用上游实测锚点投影。
// 返回 false 表示这个渠道没有配模型上限（此时只能退回字节/条数兜底）。
func cloudAgentCompactionReading(repo *repository.Repository, state *cloudAgentRuntime, canonical canonicalAgentRequest) (cloudAgentPressureReading, bool) {
	if repo == nil || state == nil {
		return cloudAgentPressureReading{}, false
	}
	if strings.TrimSpace(state.Request.ChannelID) == "" || strings.TrimSpace(state.Request.ChannelModelKey) == "" {
		return cloudAgentPressureReading{}, false
	}
	channelModel, err := repo.ChannelModelByKey(state.Request.ChannelID, state.Request.ChannelModelKey)
	if err != nil || channelModel == nil {
		return cloudAgentPressureReading{}, false
	}
	config, err := normalizedChannelModelCapability(channelModel)
	if err != nil || config == nil || config.Text == nil {
		return cloudAgentPressureReading{}, false
	}
	usable := config.Text.ContextWindowTokens - config.Text.ReservedOutputTokens
	if usable <= 0 {
		return cloudAgentPressureReading{}, false
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return cloudAgentPressureReading{}, false
	}
	projected, source := cloudAgentProjectedInputTokens(cloudAgentContextPressure{EstimatedInputTokens: estimateCloudAgentTokens(raw)}, state)
	return cloudAgentPressureReading{
		ProjectedTokens:   projected,
		UsableInputTokens: usable,
		Ratio:             float64(projected) / float64(usable),
		TokenSource:       source,
	}, true
}

// cloudAgentCompactionDecision 是压缩与否的唯一判据入口：
// 配了模型上限就按 token 利用率（80%），没配才退回字节/条数兜底——
// 两条口径不能各说各话，否则界面显示 79% 而后台已经在压。
func cloudAgentCompactionDecision(repo *repository.Repository, state *cloudAgentRuntime, canonical canonicalAgentRequest) (bool, int, int, cloudAgentPressureReading, bool) {
	if state == nil || state.ContextCompaction != nil {
		return false, 0, 0, cloudAgentPressureReading{}, false
	}
	raw, err := json.Marshal(canonical.Messages)
	if err != nil {
		return false, 0, 0, cloudAgentPressureReading{}, false
	}
	turnCount := cloudAgentConversationTurnCount(state.Canonical.Messages)
	if reading, ok := cloudAgentCompactionReading(repo, state, canonical); ok {
		return reading.Ratio >= cloudAgentCompactionRatio, len(raw), turnCount, reading, true
	}
	needed, sourceBytes, turns := cloudAgentContextShouldCompact(state)
	return needed, sourceBytes, turns, cloudAgentPressureReading{}, false
}

// cloudAgentCompactionEventPayload 是 context_compaction_requested 的载荷：
// 带上触发读数，界面才能说清"到了多少、按哪个口径"。
func cloudAgentCompactionEventPayload(reading cloudAgentPressureReading, hasReading bool, sourceBytes, turnCount int) map[string]any {
	payload := map[string]any{"sourceBytes": sourceBytes, "turnCount": turnCount, "reason": "threshold"}
	if !hasReading {
		payload["basis"] = "bytes"
		return payload
	}
	payload["basis"] = "tokens"
	payload["projectedTokens"] = reading.ProjectedTokens
	payload["usableInputTokens"] = reading.UsableInputTokens
	payload["thresholdRatio"] = cloudAgentCompactionRatio
	payload["tokenSource"] = reading.TokenSource
	payload["pressureRatio"] = math.Round(reading.Ratio*10000) / 10000
	return payload
}

// cloudAgentRequestCompaction 在下一步请求达到阈值时【暂停步进循环】：
// 先把历史压成检查点，压完再用压缩后的上下文继续本轮，而不是带着 80%+ 的上下文再发一次。
// 返回 true 表示已经请求压缩，调用方应落库并结束这一步。
func (s *Service) cloudAgentRequestCompaction(run *model.CloudAgentExecution, state *cloudAgentRuntime, canonical canonicalAgentRequest) (bool, error) {
	if run == nil || state == nil || state.ContextCompaction != nil {
		return false, nil
	}
	// 还有在跑的任务时先不评估：等它回来（校验规则也要求 requested 状态下没有活跃任务）。
	if state.ActiveTaskID != "" {
		return false, nil
	}
	if state.ContextCompactionCount >= cloudAgentMaxCompactionsPerRun {
		return false, nil
	}
	needed, sourceBytes, turnCount, reading, hasReading := cloudAgentCompactionDecision(s.repo, state, canonical)
	if !needed {
		return false, nil
	}
	state.ContextCompaction = &cloudAgentContextCompaction{
		Status: "requested", SourceBytes: sourceBytes, TurnCount: turnCount, Resume: true,
		ProjectedTokens: reading.ProjectedTokens, UsableInputTokens: reading.UsableInputTokens,
		Ratio: reading.Ratio, TokenSource: reading.TokenSource,
	}
	state.event(run.ID, "context_compaction_requested", cloudAgentCompactionEventPayload(reading, hasReading, sourceBytes, turnCount))
	return true, s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	})
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
		// 中途暂停压缩：压完继续本轮的步进；收尾压缩才结束本轮。
		resume := state.ContextCompaction != nil && state.ContextCompaction.Resume
		state.ContextCompaction = nil
		if resume {
			state.ContextCompactionCount++
			if current.Status != "cancelled" && current.Status != "failed" {
				current.Status = "running"
			}
		} else {
			current.Status = "completed"
		}
		payload := map[string]any{"mode": mode, "compactedTurnCount": checkpoint.CompactedTurnCount, "historyMessages": len(history), "resume": resume}
		if reason != "" {
			payload["reason"] = reason
		}
		state.event(run.ID, "context_compacted", payload)
		return cloudAgentSave(current, state)
	})
}
