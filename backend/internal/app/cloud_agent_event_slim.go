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
//
// cloudAgentEventPayloadLimitBytes 是单条事件载荷的硬上限（与 runtime 校验共用）：
// 超过它的载荷无法通过状态校验，历史治理必须在保存前把它降下来，而不是让整轮判死。
const cloudAgentEventPayloadLimitBytes = 128 << 10

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

	// 0) 单条载荷超过硬上限：先丢画布增量（客户端改为拉全量），再丢其它大字段；
	//    仍然超限就整条压成回执。任何一步都不让整轮因为"一条事件太胖"而失败。
	for index := range state.Events {
		event := &state.Events[index]
		if len(mustMarshalPayload(event.Payload)) <= cloudAgentEventPayloadLimitBytes {
			continue
		}
		changed = true
		if _, exists := event.Payload["canvasPatch"]; exists {
			delete(event.Payload, "canvasPatch")
			event.Payload["canvasPatchSlimmed"] = true
			event.Payload["requiresRefresh"] = true
		}
		for _, key := range []string{"result", "arguments", "preview", "breakdown", "nodes", "tools"} {
			delete(event.Payload, key)
		}
		if len(mustMarshalPayload(event.Payload)) > cloudAgentEventPayloadLimitBytes {
			receipt := map[string]any{
				"requiresRefresh": true,
				"payloadSlimmed":  true,
				"eventId":         event.EventID,
			}
			if text, ok := event.Payload["text"].(string); ok {
				receipt["text"] = truncateRunes(text, 600)
			}
			if toolName, ok := event.Payload["toolName"].(string); ok {
				receipt["toolName"] = toolName
			}
			event.Payload = receipt
		}
	}

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
	slimTool := func(payload map[string]any) bool {
		dropped := false
		if result, ok := payload["result"].(map[string]any); ok {
			// 保留 UI 与压缩事实要用到的键（nodeId / taskId / status…），其余丢掉。
			receipt := map[string]any{}
			for _, key := range cloudAgentToolReceiptKeys {
				if value, exists := result[key]; exists {
					receipt[key] = value
				}
			}
			receipt["slimmed"] = true
			payload["result"] = receipt
			dropped = true
		} else if _, exists := payload["result"]; exists {
			payload["result"] = map[string]any{"slimmed": true}
			dropped = true
		}
		if text, ok := payload["arguments"].(string); ok && len(text) > 0 {
			payload["arguments"] = ""
			dropped = true
		}
		return dropped
	}
	slimByType("tool_completed", cloudAgentToolEventKeep, slimTool)
	slimByType("tool_failed", cloudAgentToolEventKeep, slimTool)
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
	// 注意：canvasPatch 是**增量**（每步的 before/after 差量），客户端按顺序套用，
	// 少一份就会漏掉那一步的画布变更（实测 generate_media 的 draft→submit→complete
	// 三步都必须保留）。所以正常路径不丢增量，只在 level 3 的极端压力下降级。
	cloudAgentCanvasPatchKeep = 3
	// cloudAgentPressureKeep 是保留完整占用分布的份数：只有最新一次读数会被弹窗使用。
	cloudAgentPressureKeep = 1
	// cloudAgentApprovalKeep 是保留完整审批参数的份数。
	cloudAgentApprovalKeep = 3
	// cloudAgentToolEventKeep 是保留完整工具回执的份数：画布状态读取类回执单条 3.7KB，
	// 而且正文在会话消息里还有一份，旧事件只留"能被 UI 与压缩事实用到的键"。
	cloudAgentToolEventKeep = 4
)

// cloudAgentToolReceiptKeys 是工具事件瘦身后仍要保留的键：
// 前端画布同步/聚焦要用 nodeId/taskId，压缩事实要用 summary/status/phase 等。
var cloudAgentToolReceiptKeys = []string{
	"nodeId", "nodeIds", "referenceNodeIds", "taskId", "title", "summary", "status", "phase", "taskSubmitted", "operation",
}

const (
	// cloudAgentStateHardLimitBytes 是运行态硬上限：落库前测量，超过就终止本轮。
	cloudAgentStateHardLimitBytes = 512 << 10
	// 降级阶梯：越接近上限裁得越狠，但**不杀死本轮**。
	cloudAgentDegradeLevel2Ratio = 0.6
	cloudAgentDegradeLevel3Ratio = 0.85
	// level 3 之外仍保留完整载荷的最近事件：条数与体积双重封顶，
	// 只按条数会在载荷密集的运行（分镜/审批/整份画布差量）里留下几百 KB。
	cloudAgentEventFloorKeep  = 120
	cloudAgentEventFloorBytes = 120 << 10
	// level 2 起每步思考留痕的截断长度。
	cloudAgentReasoningMessageLimit = 2000
)

// cloudAgentEventHistoryDegradeLevel 按已编码体积给出需要的降级等级：0 不降级、2 轻度、3 重度。
func cloudAgentEventHistoryDegradeLevel(sizeBytes int) int {
	limit := float64(cloudAgentStateHardLimitBytes)
	switch {
	case float64(sizeBytes) >= limit*cloudAgentDegradeLevel3Ratio:
		return 3
	case float64(sizeBytes) >= limit*cloudAgentDegradeLevel2Ratio:
		return 2
	default:
		return 0
	}
}

// cloudAgentDegradeEventHistory 按等级进一步裁剪事件载荷，**始终保留
// type/seq/eventId/createdAt**（游标与回放语义不变），只在体积逼近硬上限时生效：
//
//	level 2：旧思考留痕截断到 cloudAgentReasoningMessageLimit；旧 canvas_updated 去掉 preview。
//	level 3：除最近 cloudAgentEventFloorKeep 条外，事件载荷压成一行回执，
//	         但保留 UI/压缩事实要用的键（工具名、节点、任务、状态、决定…）。
//
// 长流程因此是"变淡"而不是"突然死"。
func cloudAgentDegradeEventHistory(state *cloudAgentRuntime, level int) bool {
	if state == nil || level < 2 || len(state.Events) == 0 {
		return false
	}
	// 状态级标记：同一等级不重复遍历（事件很多时逐条 marshal 是真开销）。
	// 等级只会升，新事件都追加在尾部，所以高等级会重新走一遍全部事件。
	if state.EventDegradeLevel >= level {
		return false
	}
	changed := false
	// 尾部完整载荷的起点：从最新往前累计，条数与体积任一超限就停。
	floor, keptBytes := len(state.Events), 0
	for index := len(state.Events) - 1; index >= 0; index-- {
		size := len(mustMarshalEvent(state.Events[index]))
		if len(state.Events)-index > cloudAgentEventFloorKeep || keptBytes+size > cloudAgentEventFloorBytes {
			break
		}
		keptBytes += size
		floor = index
	}
	for index := range state.Events {
		event := &state.Events[index]
		if current, ok := event.Payload["degraded"].(int); ok && current >= level {
			continue
		}
		if index >= floor && event.Payload["degraded"] == nil {
			// 最近的事件保持完整（界面正在用）。
			continue
		}
		switch level {
		case 2:
			switch event.Type {
			case "reasoning_message", "assistant_message":
				text := stringValue(event.Payload["text"])
				if len([]rune(text)) > cloudAgentReasoningMessageLimit {
					event.Payload["text"] = truncateRunes(text, cloudAgentReasoningMessageLimit)
					event.Payload["degraded"] = 2
					changed = true
				}
			case "canvas_updated":
				if _, exists := event.Payload["preview"]; exists {
					delete(event.Payload, "preview")
					event.Payload["degraded"] = 2
					changed = true
				}
			}
		case 3:
			receipt := map[string]any{"degraded": 3}
			for _, key := range cloudAgentEventReceiptKeys {
				if value, exists := event.Payload[key]; exists {
					receipt[key] = value
				}
			}
			if result, ok := event.Payload["result"].(map[string]any); ok {
				for _, key := range cloudAgentToolReceiptKeys {
					if value, exists := result[key]; exists {
						receipt[key] = value
					}
				}
			}
			if text, ok := receipt["text"].(string); ok && len([]rune(text)) > 200 {
				receipt["text"] = truncateRunes(text, 200)
			}
			// 只在更小时才替换：小载荷收成回执反而更大（信封已在别处封顶）。
			if len(mustMarshalPayload(receipt)) >= len(mustMarshalPayload(event.Payload)) {
				continue
			}
			event.Payload = receipt
			changed = true
		}
	}
	if changed {
		// 记到状态上：同一等级不必每次保存都重新遍历（事件多时逐条 marshal 是真开销）。
		state.EventDegradeLevel = level
	}
	return changed
}

func mustMarshalEvent(event CloudAgentEvent) []byte {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return raw
}

func mustMarshalPayload(payload map[string]any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

// cloudAgentEventReceiptKeys 是重度降级后仍要保留的键。
var cloudAgentEventReceiptKeys = []string{
	"text", "toolName", "callId", "nodeId", "nodeIds", "taskId", "summary", "status", "phase",
	"decision", "approvalId", "operation", "mode", "turnCount", "basis", "pressureRatio",
}

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
