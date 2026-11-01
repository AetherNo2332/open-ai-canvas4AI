package app

import (
	"strings"

	"yingce/backend/internal/agentcontext"
)

// cloudAgentCompleteTurnTail 取会话尾部最近 pairs 轮**完整**对话。
//
// 按轮次裁剪时必须成立的不变量（与上游既有口径一致；轮内已不再做正文卸载，这里是压缩
// 唯一的裁剪口径）：
//   - 绝不以 assistant(tool_calls) 或 tool 回执开头：不制造"有调用没结果"的半截轮次；
//   - 带工具调用的 assistant 一律不进这里——它的调用参数与结果不在纯文本对里，
//     留下来会让下一轮收到一个永远闭合不了的调用；
//   - 检查点正文（带 checkpoint 来源标记）不算一轮，避免把摘要当成用户原话，
//     也不把运行时帧/续轮交接帧当成用户要求。
func cloudAgentCompleteTurnTail(messages []map[string]any, pairs int) []providerTextMessage {
	complete := make([]providerTextMessage, 0, max(0, pairs)*2)
	var pending *providerTextMessage
	for _, message := range messages {
		role, content := stringField(message, "role"), strings.TrimSpace(stringField(message, "content"))
		if (role != "user" && role != "assistant") || content == "" || stringField(message, cloudAgentContextSourceKey) == "runtime" || stringField(message, cloudAgentContextSourceKey) == "continuation" {
			continue
		}
		if stringField(message, cloudAgentContextSourceKey) == "checkpoint" {
			if _, err := agentcontext.ParseFrame(content); err == nil {
				continue
			}
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
	result := append([]providerTextMessage(nil), complete[start:]...)
	// 尚未收到回答的最后一条用户要求也必须原样保留；否则压缩会把当前目标
	// 只交给不可信的模型摘要，模型漏写时下一步便静默失去用户指令。
	if pending != nil {
		result = append(result, *pending)
	}
	return result
}

// cloudAgentRetainedTurnCount 数的是"压缩后仍原样留在上下文里的用户轮次"。
func cloudAgentRetainedTurnCount(retained []providerTextMessage) int {
	count := 0
	for _, message := range retained {
		if message.Role == "user" {
			count++
		}
	}
	return count
}

// cloudAgentRecentConversation 是"只留最近两轮"的语义化别名，保留给既有调用点。
func cloudAgentRecentConversation(messages []map[string]any, pairs int) []providerTextMessage {
	return cloudAgentCompleteTurnTail(messages, pairs)
}

// cloudAgentCheckpointHistory 组装压缩后的历史：检查点 + 回执 + 最近 N 对原文。
// 回执是必须的——它让检查点这段 user 消息后面紧跟 assistant，历史仍保持正常交替。
func cloudAgentCheckpointHistory(checkpoint agentcontext.Checkpoint, recent []providerTextMessage) ([]providerTextMessage, error) {
	framed, err := agentcontext.Frame(checkpoint)
	if err != nil {
		return nil, err
	}
	history := []providerTextMessage{{Role: "user", Content: framed, AgentContextSource: "checkpoint"}, {Role: "assistant", Content: agentcontext.Acknowledgement}}
	return append(history, recent...), nil
}
