package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type streamingAgentToolCall struct {
	id        string
	name      string
	arguments string
}

type streamingAgentParser struct {
	protocol     string
	buffer       string
	text         strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*streamingAgentToolCall
	toolCallByID map[string]int
	completed    map[string]interface{}
	// stopReason 是上游的终止原因原文（OpenAI finish_reason / Claude stop_reason / Responses status）。
	// 它决定这一步是"说完了"还是"被截断了"，运行时按它分支，不再靠匹配错误字符串倒推。
	stopReason string
	// terminalEventSeen / streamDoneSeen 是**解析事实**：前者表示收到该协议的终态事件，
	// 后者表示收到该协议的流结束标记（OpenAI `[DONE]`、Claude `message_stop`、Responses 终态事件）。
	// 它们不能由归一化 kind 反推——上游给了新词时 kind 是 unknown，但终态事件确实收到了。
	terminalEventSeen bool
	streamDoneSeen    bool
	err               error
	emit              func(string)
	emitReasoning     func(string)
	claudeThinking    map[int]map[string]interface{}
}

func newStreamingAgentParser(protocol string, emit func(string)) *streamingAgentParser {
	return &streamingAgentParser{protocol: protocol, toolCalls: map[int]*streamingAgentToolCall{}, toolCallByID: map[string]int{}, emit: emit}
}

func (p *streamingAgentParser) consume(mimeType string, chunk []byte) {
	if p == nil || p.err != nil || !strings.Contains(strings.ToLower(mimeType), "event-stream") || len(chunk) == 0 {
		return
	}
	p.buffer += string(chunk)
	p.consumeFrames(false)
}

func (p *streamingAgentParser) flush() {
	if p == nil || p.err != nil {
		return
	}
	p.consumeFrames(true)
}

func (p *streamingAgentParser) consumeFrames(flush bool) {
	for p.err == nil {
		match := sseFrameBoundaryPattern.FindStringIndex(p.buffer)
		if match == nil {
			break
		}
		p.consumeFrame(p.buffer[:match[0]])
		p.buffer = p.buffer[match[1]:]
	}
	if flush && p.err == nil && strings.TrimSpace(p.buffer) != "" {
		p.consumeFrame(p.buffer)
		p.buffer = ""
	}
}

func (p *streamingAgentParser) consumeFrame(frame string) {
	var eventName string
	var dataLines []string
	for _, line := range strings.Split(strings.ReplaceAll(frame, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	raw := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if raw == "[DONE]" {
		// OpenAI 的流结束标记：它本身不是终止原因，但"流被正常收尾"是独立事实。
		p.streamDoneSeen = true
		return
	}
	if raw == "" {
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		p.err = fmt.Errorf("Agent 流式事件解析失败：%w", err)
		return
	}
	if err := validateTextPayload(payload); err != nil {
		p.err = err
		return
	}
	switch p.protocol {
	case "responses":
		p.consumeResponsesEvent(eventName, payload)
	case "claude-api":
		p.consumeClaudeEvent(payload)
	default:
		p.consumeChatCompletionEvent(payload)
	}
}

func (p *streamingAgentParser) consumeResponsesEvent(eventName string, payload map[string]interface{}) {
	eventType := firstNonEmptyString(strings.TrimSpace(eventName), stringField(payload, "type"))
	switch eventType {
	case "response.output_text.delta", "output_text.delta":
		p.appendText(stringField(payload, "delta"))
	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		p.appendReasoning(stringField(payload, "delta"))
	case "response.completed", "response.incomplete", "response.failed":
		response, _ := payload["response"].(map[string]interface{})
		if eventType == "response.completed" {
			p.completed = response
		}
		// 只有 status=completed 才算"说完了"；incomplete 要再读 incomplete_details.reason
		// 才能分出"输出到顶"与"内容过滤"。failed/cancelled 是这次调用没成功：按解析错误处理，
		// 走既有 model_failure 路径，不能让它在运行时被当成"正常结束但没给原因"。
		status := firstNonEmptyString(stringField(response, "status"), eventType)
		if cloudAgentResponsesStatusFailed(status) {
			p.err = fmt.Errorf("上游返回 %s，这一步没有完成", status)
			return
		}
		p.terminalEventSeen = true
		p.streamDoneSeen = true
		if reason := cloudAgentResponsesStopReason(status, response); reason != "" {
			p.stopReason = reason
		}
	case "response.output_item.added":
		item, _ := payload["item"].(map[string]interface{})
		if stringField(item, "type") == "function_call" {
			index := intField(payload, "output_index", len(p.toolCalls))
			p.setToolCall(index, firstNonEmptyString(stringField(item, "call_id"), stringField(item, "id")), stringField(item, "name"), stringField(item, "arguments"))
		}
	case "response.function_call_arguments.delta":
		index := p.responseToolCallIndex(payload)
		p.toolCall(index).arguments += stringField(payload, "delta")
	case "response.function_call_arguments.done":
		index := p.responseToolCallIndex(payload)
		if arguments := stringField(payload, "arguments"); arguments != "" {
			p.toolCall(index).arguments = arguments
		}
	}
}

func (p *streamingAgentParser) responseToolCallIndex(payload map[string]interface{}) int {
	itemID := firstNonEmptyString(stringField(payload, "item_id"), stringField(payload, "call_id"))
	if index, ok := p.toolCallByID[itemID]; ok {
		return index
	}
	return intField(payload, "output_index", len(p.toolCalls))
}

func (p *streamingAgentParser) consumeChatCompletionEvent(payload map[string]interface{}) {
	choices, _ := payload["choices"].([]interface{})
	for _, value := range choices {
		choice, _ := value.(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		p.appendText(streamContentText(delta["content"]))
		p.appendReasoning(firstNonEmptyString(stringField(delta, "reasoning_content"), stringField(delta, "reasoning"), stringField(delta, "reasoning_text")))
		for fallbackIndex, toolValue := range interfaceSlice(delta["tool_calls"]) {
			tool, _ := toolValue.(map[string]interface{})
			index := intField(tool, "index", fallbackIndex)
			function, _ := tool["function"].(map[string]interface{})
			current := p.toolCall(index)
			if id := stringField(tool, "id"); id != "" {
				current.id = id
				p.toolCallByID[id] = index
			}
			if name := stringField(function, "name"); name != "" {
				current.name += name
			}
			current.arguments += stringField(function, "arguments")
		}
		// 终止块才带非空 finish_reason（tool_calls/length/stop/…）；前面的 chunk 一律是 null，
		// 所以每次非空就覆盖，留到流结束时用的就是最后一个有效值。
		if reason := strings.TrimSpace(stringField(choice, "finish_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	}
}

func (p *streamingAgentParser) consumeClaudeEvent(payload map[string]interface{}) {
	switch stringField(payload, "type") {
	case "content_block_start":
		block, _ := payload["content_block"].(map[string]interface{})
		index := intField(payload, "index", len(p.toolCalls))
		switch stringField(block, "type") {
		case "text":
			p.appendText(stringField(block, "text"))
		case "thinking":
			thinking := firstRawString(stringField(block, "thinking"), stringField(block, "text"))
			p.appendReasoning(thinking)
			p.claudeThinkingBlock(index, "thinking")["thinking"] = thinking
			p.claudeThinkingBlock(index, "thinking")["signature"] = stringField(block, "signature")
		case "redacted_thinking":
			p.claudeThinkingBlock(index, "redacted_thinking")["data"] = stringField(block, "data")
		case "tool_use":
			arguments := ""
			if input := block["input"]; input != nil {
				if encoded, err := json.Marshal(input); err == nil && string(encoded) != "{}" {
					arguments = string(encoded)
				}
			}
			p.setToolCall(index, stringField(block, "id"), stringField(block, "name"), arguments)
		}
	case "content_block_delta":
		delta, _ := payload["delta"].(map[string]interface{})
		index := intField(payload, "index", len(p.toolCalls)-1)
		if stringField(delta, "type") == "text_delta" {
			p.appendText(stringField(delta, "text"))
		}
		if stringField(delta, "type") == "thinking_delta" {
			thinking := firstRawString(stringField(delta, "thinking"), stringField(delta, "text"))
			p.appendReasoning(thinking)
			block := p.claudeThinkingBlock(index, "thinking")
			block["thinking"] = stringField(block, "thinking") + thinking
		}
		if stringField(delta, "type") == "signature_delta" {
			block := p.claudeThinkingBlock(index, "thinking")
			block["signature"] = stringField(block, "signature") + stringField(delta, "signature")
		}
		if stringField(delta, "type") == "input_json_delta" {
			p.toolCall(index).arguments += stringField(delta, "partial_json")
		}
	case "message_delta":
		// Claude 的终止原因在 message_delta.stop_reason：
		// max_tokens / tool_use / end_turn / stop_sequence / refusal / pause_turn。
		delta, _ := payload["delta"].(map[string]interface{})
		if reason := strings.TrimSpace(stringField(delta, "stop_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	case "message_stop":
		// Claude 的流结束标记。它可能与 stop_reason 同时出现，也可能没有（流被中断），
		// 所以"收到终态事件"与"流正常收尾"必须分开记。
		p.streamDoneSeen = true
	case "error":
		errValue, _ := payload["error"].(map[string]interface{})
		p.err = errors.New(defaultString(stringField(errValue, "message"), "Claude 上游返回失败"))
	}
}

func resultString(value map[string]interface{}, key string) string {
	return stringField(value, key)
}

func (p *streamingAgentParser) claudeThinkingBlock(index int, kind string) map[string]interface{} {
	if p.claudeThinking == nil {
		p.claudeThinking = map[int]map[string]interface{}{}
	}
	block := p.claudeThinking[index]
	if block == nil {
		block = map[string]interface{}{"type": kind}
		p.claudeThinking[index] = block
	}
	return block
}

func (p *streamingAgentParser) claudeThinkingBlocks() []interface{} {
	if len(p.claudeThinking) == 0 {
		return nil
	}
	indices := make([]int, 0, len(p.claudeThinking))
	for index := range p.claudeThinking {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	blocks := make([]interface{}, 0, len(indices))
	for _, index := range indices {
		blocks = append(blocks, p.claudeThinking[index])
	}
	return blocks
}

func (p *streamingAgentParser) appendText(delta string) {
	if delta == "" {
		return
	}
	p.text.WriteString(delta)
	if p.emit != nil {
		p.emit(delta)
	}
}

func (p *streamingAgentParser) appendReasoning(delta string) {
	if delta == "" {
		return
	}
	p.reasoning.WriteString(delta)
	if p.emitReasoning != nil {
		p.emitReasoning(delta)
	}
}

func (p *streamingAgentParser) toolCall(index int) *streamingAgentToolCall {
	if index < 0 {
		index = 0
	}
	if p.toolCalls[index] == nil {
		p.toolCalls[index] = &streamingAgentToolCall{}
	}
	return p.toolCalls[index]
}

func (p *streamingAgentParser) setToolCall(index int, id string, name string, arguments string) {
	call := p.toolCall(index)
	call.id, call.name, call.arguments = id, name, arguments
	if id != "" {
		p.toolCallByID[id] = index
	}
}

func (p *streamingAgentParser) result() (map[string]interface{}, error) {
	if p.err != nil {
		return nil, p.err
	}
	if !p.terminalEventSeen && !p.streamDoneSeen {
		return nil, errors.New("上游流式响应提前中断：未收到结束标记")
	}
	if p.completed != nil {
		result, err := parseAgentToolPayload(p.completed, p.protocol)
		if err != nil {
			return nil, err
		}
		if p.text.Len() > 0 {
			result["text"] = p.text.String()
		}
		if p.reasoning.Len() > 0 {
			result["reasoning"] = p.reasoning.String()
		}
		// 流式拿到过终止块时以它为准（parseAgentToolPayload 只能看到 completed 快照）。
		if strings.TrimSpace(p.stopReason) != "" {
			cloudAgentApplyStopReason(result, p.stopReason)
		}
		cloudAgentApplyStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
		return result, nil
	}
	result := map[string]interface{}{"mode": "text", "text": p.text.String(), "toolCalls": []interface{}{}}
	if blocks := p.claudeThinkingBlocks(); len(blocks) > 0 {
		result["reasoningBlocks"] = blocks
	}
	markAgentReasoningWire(result, p.protocol)
	if p.reasoning.Len() > 0 {
		result["reasoning"] = p.reasoning.String()
	}
	indices := make([]int, 0, len(p.toolCalls))
	for index := range p.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	calls := make([]interface{}, 0, len(indices))
	for _, index := range indices {
		call := p.toolCalls[index]
		if call == nil || strings.TrimSpace(call.id) == "" || strings.TrimSpace(call.name) == "" {
			continue
		}
		arguments := call.arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
			return nil, fmt.Errorf("Agent 工具参数不是完整 JSON：%w", err)
		}
		calls = append(calls, map[string]interface{}{"id": call.id, "type": "function", "function": map[string]interface{}{"name": call.name, "arguments": arguments}})
	}
	result["toolCalls"] = calls
	if p.text.Len() == 0 && len(calls) == 0 {
		return nil, errors.New("画布 Agent 接口没有返回内容")
	}
	cloudAgentApplyStopReason(result, p.stopReason)
	cloudAgentApplyStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
	return result, nil
}

func intField(value map[string]interface{}, key string, fallback int) int {
	number, ok := value[key].(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return fallback
	}
	return int(number)
}

func extractResponseReasoning(payload map[string]interface{}) string {
	var chunks []string
	for _, value := range interfaceSlice(payload["output"]) {
		item, _ := value.(map[string]interface{})
		if stringField(item, "type") != "reasoning" {
			continue
		}
		for _, key := range []string{"summary", "content"} {
			for _, part := range interfaceSlice(item[key]) {
				record, _ := part.(map[string]interface{})
				if text := strings.TrimSpace(stringField(record, "text")); text != "" {
					chunks = append(chunks, text)
				}
			}
		}
	}
	return strings.Join(chunks, "\n")
}

func interfaceSlice(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func cloneStringAnyMap(value map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(value)+1)
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func attachProviderPiUsage(result map[string]interface{}, usage map[string]any) {
	if len(usage) > 0 {
		result["usage"] = usage
	}
}
