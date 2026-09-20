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

// cloudAgentContextCompaction 是"暂停步进循环去压历史"的状态面：Status 为
// requested（已请求、等调度起压缩任务）/ running（压缩任务已发出）。
type cloudAgentContextCompaction struct {
	Status      string `json:"status"`
	SourceBytes int    `json:"sourceBytes"`
	TurnCount   int    `json:"turnCount"`
	// Resume 表示这次是"中途暂停压缩"：压完继续本轮的步进，而不是收尾结束本轮。
	Resume bool `json:"resume,omitempty"`
	// 触发读数：下一步预计输入 token ÷ 模型可用输入（上游实测锚点优先）。
	ProjectedTokens   int     `json:"projectedTokens,omitempty"`
	UsableInputTokens int     `json:"usableInputTokens,omitempty"`
	Ratio             float64 `json:"ratio,omitempty"`
	TokenSource       string  `json:"tokenSource,omitempty"`
}

// cloudAgentCompactionRatio / cloudAgentCompactionPercent 定义在
// cloud_agent_usage_anchor.go：压力读数、压缩判据与预算算式共用同一条线，
// 否则会出现"界面显示 84%、后台已经在压"。
const (
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
	// CompactAtTokens / OverheadTokens / BudgetSource 说明"这条线是按哪个算式算出来的"。
	CompactAtTokens int    `json:"compactAtTokens,omitempty"`
	OverheadTokens  int    `json:"overheadTokens,omitempty"`
	BudgetSource    string `json:"budgetSource,omitempty"`
}

// cloudAgentCompactionReading 用 token meter 的口径算"离压缩还有多远"：
// 上限取输入预算（窗口 − 输出预留 − overhead，见 cloudAgentContextBudgetForRequest），
// 当前值优先用上游实测锚点投影。
// 返回 false 表示这个渠道没有配模型上限（此时只能退回字节/条数兜底）。
func cloudAgentCompactionReading(s *Service, state *cloudAgentRuntime, canonical canonicalAgentRequest) (cloudAgentPressureReading, bool) {
	if s == nil || state == nil {
		return cloudAgentPressureReading{}, false
	}
	budget, ok := s.cloudAgentResolvedContextBudget(state.Request)
	if !ok {
		return cloudAgentPressureReading{}, false
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return cloudAgentPressureReading{}, false
	}
	projected, source := cloudAgentProjectedInputTokens(cloudAgentContextPressure{EstimatedInputTokens: estimateCloudAgentTokens(raw)}, state)
	return cloudAgentPressureReading{
		ProjectedTokens:   projected,
		UsableInputTokens: budget.InputBudgetTokens,
		Ratio:             float64(projected) / float64(budget.InputBudgetTokens),
		TokenSource:       source,
		CompactAtTokens:   budget.CompactAtTokens,
		OverheadTokens:    budget.OverheadTokens,
		BudgetSource:      budget.Source,
	}, true
}

// cloudAgentCompactionDecision 是压缩与否的唯一判据入口：
// 配了模型上限就按输入预算的 token 利用率（85%），没配才退回字节/条数兜底——
// 两条口径不能各说各话，否则界面显示 84% 而后台已经在压。
func cloudAgentCompactionDecision(s *Service, state *cloudAgentRuntime, canonical canonicalAgentRequest) (bool, int, int, cloudAgentPressureReading, bool) {
	if state == nil || state.ContextCompaction != nil {
		return false, 0, 0, cloudAgentPressureReading{}, false
	}
	raw, err := json.Marshal(canonical.Messages)
	if err != nil {
		return false, 0, 0, cloudAgentPressureReading{}, false
	}
	turnCount := cloudAgentConversationTurnCount(state.Canonical.Messages)
	if reading, ok := cloudAgentCompactionReading(s, state, canonical); ok {
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
	if reading.CompactAtTokens > 0 {
		payload["compactAtTokens"] = reading.CompactAtTokens
	}
	if reading.OverheadTokens > 0 {
		payload["overheadTokens"] = reading.OverheadTokens
	}
	if reading.BudgetSource != "" {
		payload["budgetSource"] = reading.BudgetSource
	}
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
	needed, sourceBytes, turnCount, reading, hasReading := cloudAgentCompactionDecision(s, state, canonical)
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
	// 锚点不再携带权限与"可自行决定"清单（用户消息定义目标），这里只把跨轮必须记住的
	// 视觉事实带进收束摘要：下一轮才不会再声称"没有视觉识别证据"并重复看图。
	for _, asset := range state.CreativeAnchor.ReferenceAssets {
		if asset.VisualIdentity != "inspected" {
			continue
		}
		observed := fmt.Sprintf("已查看过画面 %s（%s），无需重复看图", asset.NodeID, asset.Type)
		if note := strings.TrimSpace(asset.VisualNote); note != "" {
			observed = fmt.Sprintf("已查看过画面 %s（%s）：%s", asset.NodeID, asset.Type, truncateRunes(note, 600))
		}
		checkpoint.Decisions = append(checkpoint.Decisions, observed)
	}
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

// cloudAgentCompleteTurnTail 取会话尾部最近 pairs 轮**完整**对话。
//
// 按轮次裁剪时必须成立的不变量（上游同口径；轮内已不再做正文卸载，这里是压缩唯一的裁剪口径）：
//   - 绝不以 assistant(tool_calls) 或 tool 回执开头：不制造"有调用没结果"的半截轮次；
//   - 带工具调用的 assistant 一律不进这里——它的调用参数与结果不在 TextHistory 里，
//     留下来会让下一轮收到一个永远闭合不了的调用；
//   - 检查点正文（<agent-context-checkpoint>）不算一轮，避免把摘要当成用户原话。
func cloudAgentCompleteTurnTail(messages []map[string]any, pairs int) []providerTextMessage {
	complete := make([]providerTextMessage, 0, max(0, pairs)*2)
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
	start := max(0, len(complete)-max(0, pairs)*2)
	return append([]providerTextMessage(nil), complete[start:]...)
}

// cloudAgentRecentConversation 是"只留最近两轮"的语义化别名，保留给既有调用点。
func cloudAgentRecentConversation(messages []map[string]any, pairs int) []providerTextMessage {
	return cloudAgentCompleteTurnTail(messages, pairs)
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
	return s.writeCloudAgentContextCheckpoint(run, state, checkpoint, mode, reason, false)
}

// finalizeCloudAgentInterruptedCompaction 收拾"停在压缩上"的终态轮次。
//
// 压缩是「暂停步进 → 压完继续」的中途动作：本轮在压缩期间被取消（或已经失败）时，
// 调度器不会再推进这一轮，于是 ContextCompaction 会永远挂在 requested/running 上
// （界面一直显示"正在压缩"），那份检查点也永远落不了盘。这里按服务端保底检查点收口。
func (s *Service) finalizeCloudAgentInterruptedCompaction(run *model.CloudAgentExecution, state *cloudAgentRuntime, reason string) error {
	if run == nil || state == nil || state.ContextCompaction == nil {
		return nil
	}
	checkpoint := cloudAgentFallbackCheckpoint(state)
	checkpoint.CompactedTurnCount = state.ContextCompaction.TurnCount
	return s.writeCloudAgentContextCheckpoint(run, state, checkpoint, "fallback", reason, true)
}

// writeCloudAgentContextCheckpoint 是落检查点的唯一实现。
//
// keepTerminal=true 用于「本轮已经是终态」的收尾：只落检查点、历史与事件，不改运行状态
// （否则一次失败轮的收尾会把 failed 写成 completed，等于凭空复活一轮）。
func (s *Service) writeCloudAgentContextCheckpoint(run *model.CloudAgentExecution, state *cloudAgentRuntime, checkpoint agentcontext.Checkpoint, mode, reason string, keepTerminal bool) error {
	checkpoint = cloudAgentBoundCheckpoint(checkpoint)
	turnsBefore := cloudAgentConversationTurnCount(state.Canonical.Messages)
	recent := cloudAgentCompleteTurnTail(state.Canonical.Messages, 2)
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
		// 消息整体被替换：写路径按 kind 逐条 upsert 并删除 sequence 超出新条数的尾部行，
		// 因此"条数恰好相同但内容全变"也能正确落库。
		// 历史被换成检查点，早期读取回执也一起没了：不清"已读过去重"标记，模型再要技能正文
		// 会被回一句"本轮已请求过该技能路径，请使用历史工具结果"，而那份历史已经不存在。
		state.SkillReads, state.ProfileReads = nil, nil
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
		} else if !keepTerminal {
			current.Status = "completed"
		}
		turnsAfter := cloudAgentConversationTurnCount(state.Canonical.Messages)
		payload := map[string]any{
			"mode": mode, "compactedTurnCount": checkpoint.CompactedTurnCount,
			"historyMessages": len(history), "resume": resume,
			// 被折进检查点的轮次数：界面与排查都要能知道"这次压掉了多少历史"。
			"droppedTurns": max(0, turnsBefore-turnsAfter),
		}
		if reason != "" {
			payload["reason"] = reason
		}
		state.event(run.ID, "context_compacted", payload)
		return cloudAgentSave(current, state)
	})
}
