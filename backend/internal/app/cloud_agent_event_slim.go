package app

import "encoding/json"

// 事件日志是 state_json 的主要增长源：实测一轮 16 步的状态 504,925 字节里，
// 事件占 314,514 **字符**（消息只有 36,659 字符），顶破 512KB 守卫就会让整轮失败
// （07:41 那轮就是这么死的："Agent 上下文或执行记录超过安全限制"）。
//
// cloudAgentSlimEventHistory 只删"别处已有权威副本"或"已被后一条事件取代"的字段，
// 并且**绝不动事件的顺序、seq 与 EventID**——SSE 断线重连的游标就是 seq，
// 挪动它会打断进行中的回放。四类削减：
//
//  1. 流式增量：某一步的 reasoning_message / assistant_message 到达后，该步之前的
//     reasoning_delta / assistant_delta 文本就没有信息量了（界面回放用聚合消息，
//     且聚合消息会整段替换同 messageId 的文本）；
//  2. canvas_updated.canvasPatch：是**三方增量**（before/after），前端画布同步要用，
//     所以只保留最近 cloudAgentCanvasPatchKeep 份，更早的丢掉后重连客户端会走
//     已有的 refresh 兜底拿全量画布；
//  3. context_pressure.breakdown：占用分布体积最大且只服务"最新一次读数"（弹窗），
//     历史步只留数字；
//  4. 审批的 arguments/preview：审批卡只在待决时需要；已决的旧审批保留人类可读的
//     preview 与决定，去掉原始 arguments（媒体审批例外：客户端的设置比对要用它）。
func cloudAgentSlimEventHistory(state *cloudAgentRuntime, terminal bool) bool {
	if state == nil || len(state.Events) == 0 {
		return false
	}
	changed := false

	// 1) 流式增量：按"聚合消息已到达"为界清空文本。
	// 终态（completed/failed/cancelled）时最后一步的增量没有聚合消息可依（实测那轮就是
	// 中途失败，尾步 74KB 增量文本永远留在状态里），此时全部清掉——回放只服务历史展示。
	trimDeltas := func(deltaType, summaryType string) {
		latest := 0
		for _, event := range state.Events {
			if event.Type == summaryType && event.Seq > latest {
				latest = event.Seq
			}
		}
		if latest == 0 && !terminal {
			return
		}
		for index := range state.Events {
			event := &state.Events[index]
			if event.Type != deltaType || (!terminal && event.Seq >= latest) || event.Payload["trimmed"] == true {
				continue
			}
			if text, ok := event.Payload["text"].(string); ok && text != "" {
				event.Payload["text"] = ""
				event.Payload["trimmed"] = true
				changed = true
			}
		}
	}
	trimDeltas("reasoning_delta", "reasoning_message")
	trimDeltas("assistant_delta", "assistant_message")

	// 2) canvas_updated / 3) context_pressure / 4) 审批：按事件类型保留最近 N 份完整载荷。
	slimByType := func(eventType string, keep int, drop func(payload map[string]any) bool) {
		indices := make([]int, 0, 8)
		for index := range state.Events {
			if state.Events[index].Type == eventType {
				indices = append(indices, index)
			}
		}
		if len(indices) <= keep {
			return
		}
		for _, index := range indices[:len(indices)-keep] {
			if drop(state.Events[index].Payload) {
				changed = true
			}
		}
	}
	slimByType("canvas_updated", cloudAgentCanvasPatchKeep, func(payload map[string]any) bool {
		if _, exists := payload["canvasPatch"]; !exists {
			return false
		}
		delete(payload, "canvasPatch")
		payload["canvasPatchSlimmed"] = true
		return true
	})
	slimByType("context_pressure", cloudAgentPressureKeep, func(payload map[string]any) bool {
		if _, exists := payload["breakdown"]; !exists {
			return false
		}
		delete(payload, "breakdown")
		payload["breakdownSlimmed"] = true
		return true
	})
	slimApproval := func(payload map[string]any) bool {
		// 媒体审批的原始参数仍要留给客户端做设置比对（agentApprovalMatchesSettings）。
		if stringValue(payload["toolName"]) == "generate_media" {
			return false
		}
		// preview（人类可读摘要）保留，只清掉重复的原始 arguments。
		// 注意 arguments 在内存里既可能是 string，也可能是 json.RawMessage
		// （审批路径直接塞的是 RawMessage，实测字符串断言会漏掉它）。
		switch value := payload["arguments"].(type) {
		case nil:
			return false
		case string:
			if value == "" {
				return false
			}
		case json.RawMessage:
			if len(value) == 0 {
				return false
			}
		}
		payload["arguments"] = ""
		payload["argumentsSlimmed"] = true
		return true
	}
	slimByType("approval_requested", cloudAgentApprovalKeep, slimApproval)
	slimByType("approval_decided", cloudAgentApprovalKeep, func(payload map[string]any) bool {
		if stringValue(payload["toolName"]) == "generate_media" {
			return false
		}
		dropped := slimApproval(payload)
		if _, exists := payload["preview"]; exists {
			delete(payload, "preview")
			payload["previewSlimmed"] = true
			dropped = true
		}
		return dropped
	})
	return changed
}

const (
	// cloudAgentCanvasPatchKeep 是保留完整画布增量的份数：前端画布同步按顺序套用增量，
	// 丢掉更早的会让重连客户端落到"拉全量画布"的兜底路径（已实现且结果一致）。
	cloudAgentCanvasPatchKeep = 3
	// cloudAgentPressureKeep 是保留完整占用分布的份数：只有最新一次读数会被弹窗使用。
	cloudAgentPressureKeep = 1
	// cloudAgentApprovalKeep 是保留完整审批参数的份数。
	cloudAgentApprovalKeep = 3
)

// cloudAgentEventHistoryBytes 返回事件日志的编码体积（字节），用于观测与断言。
func cloudAgentEventHistoryBytes(state *cloudAgentRuntime) int {
	if state == nil {
		return 0
	}
	raw, err := json.Marshal(state.Events)
	if err != nil {
		return 0
	}
	return len(raw)
}
