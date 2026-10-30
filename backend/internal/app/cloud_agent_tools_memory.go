package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// memory_update 的执行体（上游 cloud_agent_tools_memory.go 的记忆部分，接到 fork 运行时）。
// 记忆写入不是画布写入：不进审批链、不走画布快照；模式（auto/review/off）以记忆文件当前
// 设置为准，用户在运行中改了模式也立即生效。只读与子代理运行不暴露这个工具。

func cloudAgentMemoryChangeItems(changes []AgentMemoryDocChange) []any {
	items := make([]any, 0, len(changes))
	for _, change := range changes {
		item := map[string]any{"kind": change.Kind, "id": change.ID, "section": change.Section}
		if change.Before != "" {
			item["before"] = change.Before
		}
		if change.After != "" {
			item["after"] = change.After
		}
		items = append(items, item)
	}
	return items
}

func (s *Service) cloudAgentHandleMemoryUpdate(_ *repository.Repository, userID, runID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	if state == nil {
		return nil, BadAuthRequest("当前运行状态无效")
	}
	if state.Request.PermissionMode == "read_only" {
		return nil, BadAuthRequest("只读模式不能修改长期记忆")
	}
	if mode := strings.TrimSpace(state.Request.MemoryMode); mode == "" || mode == model.AgentMemoryDocumentModeOff {
		return nil, BadAuthRequest("本轮没有开启长期记忆，不要调用 memory_update")
	}
	if state.MemoryWriteCalls >= agentMemoryCallsPerRun {
		return nil, BadAuthRequest(fmt.Sprintf("本轮修改记忆已达 %d 次上限；需要的修改请合并到一次调用里", agentMemoryCallsPerRun))
	}
	var args struct {
		Operations []agentMemoryOperation `json:"operations"`
		Reason     string                 `json:"reason"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if utf8.RuneCountInString(args.Reason) > agentMemoryReasonMaxRunes {
		return nil, BadAuthRequest(fmt.Sprintf("reason 不超过 %d 字", agentMemoryReasonMaxRunes))
	}
	if strings.TrimSpace(args.Reason) != "" {
		if _, err := agentMemoryValidateText(args.Reason, "reason"); err != nil {
			return nil, err
		}
	}
	state.MemoryWriteCalls++
	outcome, detail, err := s.agentMemoryAgentWrite(userID, runID, call.ID, args.Operations, args.Reason)
	if err != nil {
		if detail != nil {
			return detail, err
		}
		return nil, err
	}
	result := map[string]any{
		"status":  outcome.Status,
		"usage":   fmt.Sprintf("%d/%d", outcome.CharCount, outcome.Capacity),
		"changes": cloudAgentMemoryChangeItems(outcome.Changes),
	}
	if outcome.Duplicates > 0 {
		result["duplicates"] = outcome.Duplicates
	}
	switch outcome.Status {
	case agentMemoryOutcomeApplied:
		result["text"] = "已写入长期记忆，下一轮对话起出现在记忆快照里；本轮快照不会刷新，以本结果为准。用户可以在聊天里撤销。"
	case agentMemoryOutcomePending:
		result["text"] = "已提交给用户确认，确认前不会生效；不要对用户说已经记住。"
	default:
		result["text"] = "没有变化：这些内容已经在记忆里。"
		return result, nil
	}
	event := map[string]any{
		"status": outcome.Status, "revisionId": outcome.RevisionID, "summary": outcome.Summary,
		"changes": cloudAgentMemoryChangeItems(outcome.Changes), "charCount": outcome.CharCount, "capacity": outcome.Capacity,
		"callId": call.ID, "text": "长期记忆已更新",
	}
	if outcome.Status == agentMemoryOutcomePending {
		event["text"] = "Agent 想修改长期记忆，等你确认"
	}
	state.event(runID, "memory_updated", event)
	return result, nil
}
