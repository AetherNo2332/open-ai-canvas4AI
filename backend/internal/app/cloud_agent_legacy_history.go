package app

// 旧运行记录没有单独保存历史；只从模型请求中当前用户消息之前的严格交替前缀恢复。
func cloudAgentLegacyHistory(messages []map[string]interface{}, currentPrompt string) []providerTextMessage {
	currentIndex := -1
	for index, message := range messages {
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		if role != "user" && role != "assistant" {
			break
		}
		if role == "user" && content == currentPrompt {
			currentIndex = index
		}
	}
	if currentIndex < 0 || currentIndex%2 != 0 {
		return nil
	}
	history := make([]providerTextMessage, 0, currentIndex)
	for index := 0; index < currentIndex; index++ {
		role, _ := messages[index]["role"].(string)
		content, ok := messages[index]["content"].(string)
		if !ok || role != []string{"user", "assistant"}[index%2] {
			return nil
		}
		// 来源标记必须一起搬：丢了它，旧会话里的运行时交接消息会被当成真人轮次参与裁剪。
		message := providerTextMessage{Role: role, Content: content}
		if source, ok := messages[index][cloudAgentContextSourceKey].(string); ok && source != "" && source != "runtime" {
			message.AgentContextSource = source
		}
		history = append(history, message)
	}
	return history
}
