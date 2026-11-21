package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var textAgentFrameBoundaryPattern = regexp.MustCompile(`\r?\n\r?\n`)

// TextAgentStreamOptions adapts host-owned payload validation and non-stream
// result normalization without coupling the parser to app error types.
type TextAgentStreamOptions struct {
	Wire            string
	EmitText        func(string)
	EmitReasoning   func(string)
	ValidatePayload func(json.RawMessage) error
	ParseCompleted  func(json.RawMessage) (json.RawMessage, error)
}

type textAgentToolCall struct {
	id        string
	name      string
	arguments string
}

// TextAgentStreamParser owns the SSE frame and provider event state machines
// for Chat Completions, Responses and Claude Messages streams.
type TextAgentStreamParser struct {
	wire              string
	buffer            string
	text              strings.Builder
	reasoning         strings.Builder
	toolCalls         map[int]*textAgentToolCall
	toolCallByID      map[string]int
	completed         map[string]interface{}
	stopReason        string
	terminalEventSeen bool
	streamDoneSeen    bool
	err               error
	options           TextAgentStreamOptions
	claudeThinking    map[int]map[string]interface{}
}

// NewTextAgentStreamParser creates a parser for one known text wire.
func NewTextAgentStreamParser(options TextAgentStreamOptions) *TextAgentStreamParser {
	return &TextAgentStreamParser{
		wire:         options.Wire,
		toolCalls:    map[int]*textAgentToolCall{},
		toolCallByID: map[string]int{},
		options:      options,
	}
}

// Consume adds a chunk when the response is an SSE stream.
func (p *TextAgentStreamParser) Consume(mimeType string, chunk []byte) {
	if p == nil || p.err != nil || !strings.Contains(strings.ToLower(mimeType), "event-stream") || len(chunk) == 0 {
		return
	}
	p.buffer += string(chunk)
	p.consumeFrames(false)
}

// Flush consumes a final frame that was not followed by a blank line.
func (p *TextAgentStreamParser) Flush() {
	if p == nil || p.err != nil {
		return
	}
	p.consumeFrames(true)
}

func (p *TextAgentStreamParser) consumeFrames(flush bool) {
	for p.err == nil {
		match := textAgentFrameBoundaryPattern.FindStringIndex(p.buffer)
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

func (p *TextAgentStreamParser) consumeFrame(frame string) {
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
	if p.options.ValidatePayload != nil {
		if err := p.options.ValidatePayload(json.RawMessage(raw)); err != nil {
			p.err = err
			return
		}
	} else if err := defaultTextAgentPayloadError(payload); err != nil {
		p.err = err
		return
	}
	switch p.wire {
	case TextWireResponses:
		p.consumeResponsesEvent(eventName, payload)
	case TextWireClaude:
		p.consumeClaudeEvent(payload)
	default:
		p.consumeChatCompletionEvent(payload)
	}
}

func defaultTextAgentPayloadError(payload map[string]interface{}) error {
	if code, ok := payload["code"].(float64); ok && code != 0 {
		return errors.New(defaultTextAgentString(textAgentStringField(payload, "msg"), "请求失败"))
	}
	if errValue, ok := payload["error"].(map[string]interface{}); ok {
		if message := textAgentStringField(errValue, "message"); message != "" {
			return errors.New(message)
		}
	}
	return nil
}

func (p *TextAgentStreamParser) consumeResponsesEvent(eventName string, payload map[string]interface{}) {
	eventType := firstNonEmptyTextAgentString(strings.TrimSpace(eventName), textAgentStringField(payload, "type"))
	switch eventType {
	case "response.output_text.delta", "output_text.delta":
		p.appendText(textAgentStringField(payload, "delta"))
	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		p.appendReasoning(textAgentStringField(payload, "delta"))
	case "response.completed", "response.incomplete", "response.failed":
		response, _ := payload["response"].(map[string]interface{})
		if eventType == "response.completed" {
			p.completed = response
		}
		status := firstNonEmptyTextAgentString(textAgentStringField(response, "status"), eventType)
		if responsesStatusFailed(status) {
			p.err = fmt.Errorf("上游返回 %s，这一步没有完成", status)
			return
		}
		p.terminalEventSeen = true
		p.streamDoneSeen = true
		if reason := responsesStopReason(status, response); reason != "" {
			p.stopReason = reason
		}
	case "response.output_item.added":
		item, _ := payload["item"].(map[string]interface{})
		if textAgentStringField(item, "type") == "function_call" {
			index := textAgentIntField(payload, "output_index", len(p.toolCalls))
			p.setToolCall(index, firstNonEmptyTextAgentString(textAgentStringField(item, "call_id"), textAgentStringField(item, "id")), textAgentStringField(item, "name"), textAgentStringField(item, "arguments"))
		}
	case "response.function_call_arguments.delta":
		index := p.responseToolCallIndex(payload)
		p.toolCall(index).arguments += textAgentStringField(payload, "delta")
	case "response.function_call_arguments.done":
		index := p.responseToolCallIndex(payload)
		if arguments := textAgentStringField(payload, "arguments"); arguments != "" {
			p.toolCall(index).arguments = arguments
		}
	}
}

func (p *TextAgentStreamParser) responseToolCallIndex(payload map[string]interface{}) int {
	itemID := firstNonEmptyTextAgentString(textAgentStringField(payload, "item_id"), textAgentStringField(payload, "call_id"))
	if index, ok := p.toolCallByID[itemID]; ok {
		return index
	}
	return textAgentIntField(payload, "output_index", len(p.toolCalls))
}

func (p *TextAgentStreamParser) consumeChatCompletionEvent(payload map[string]interface{}) {
	choices, _ := payload["choices"].([]interface{})
	for _, value := range choices {
		choice, _ := value.(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		p.appendText(textAgentStreamContent(delta["content"]))
		p.appendReasoning(firstNonEmptyTextAgentString(
			textAgentStringField(delta, "reasoning_content"),
			textAgentStringField(delta, "reasoning"),
			textAgentStringField(delta, "reasoning_text"),
		))
		for fallbackIndex, toolValue := range textAgentInterfaceSlice(delta["tool_calls"]) {
			tool, _ := toolValue.(map[string]interface{})
			index := textAgentIntField(tool, "index", fallbackIndex)
			function, _ := tool["function"].(map[string]interface{})
			current := p.toolCall(index)
			if id := textAgentStringField(tool, "id"); id != "" {
				current.id = id
				p.toolCallByID[id] = index
			}
			if name := textAgentStringField(function, "name"); name != "" {
				current.name += name
			}
			current.arguments += textAgentStringField(function, "arguments")
		}
		if reason := strings.TrimSpace(textAgentStringField(choice, "finish_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	}
}

func (p *TextAgentStreamParser) consumeClaudeEvent(payload map[string]interface{}) {
	switch textAgentStringField(payload, "type") {
	case "content_block_start":
		block, _ := payload["content_block"].(map[string]interface{})
		index := textAgentIntField(payload, "index", len(p.toolCalls))
		switch textAgentStringField(block, "type") {
		case "text":
			p.appendText(textAgentStringField(block, "text"))
		case "thinking":
			thinking := firstRawTextAgentString(textAgentStringField(block, "thinking"), textAgentStringField(block, "text"))
			p.appendReasoning(thinking)
			p.claudeThinkingBlock(index, "thinking")["thinking"] = thinking
			p.claudeThinkingBlock(index, "thinking")["signature"] = textAgentStringField(block, "signature")
		case "redacted_thinking":
			p.claudeThinkingBlock(index, "redacted_thinking")["data"] = textAgentStringField(block, "data")
		case "tool_use":
			arguments := ""
			if input := block["input"]; input != nil {
				if encoded, err := json.Marshal(input); err == nil && string(encoded) != "{}" {
					arguments = string(encoded)
				}
			}
			p.setToolCall(index, textAgentStringField(block, "id"), textAgentStringField(block, "name"), arguments)
		}
	case "content_block_delta":
		delta, _ := payload["delta"].(map[string]interface{})
		index := textAgentIntField(payload, "index", len(p.toolCalls)-1)
		if textAgentStringField(delta, "type") == "text_delta" {
			p.appendText(textAgentStringField(delta, "text"))
		}
		if textAgentStringField(delta, "type") == "thinking_delta" {
			thinking := firstRawTextAgentString(textAgentStringField(delta, "thinking"), textAgentStringField(delta, "text"))
			p.appendReasoning(thinking)
			block := p.claudeThinkingBlock(index, "thinking")
			block["thinking"] = textAgentStringField(block, "thinking") + thinking
		}
		if textAgentStringField(delta, "type") == "signature_delta" {
			block := p.claudeThinkingBlock(index, "thinking")
			block["signature"] = textAgentStringField(block, "signature") + textAgentStringField(delta, "signature")
		}
		if textAgentStringField(delta, "type") == "input_json_delta" {
			p.toolCall(index).arguments += textAgentStringField(delta, "partial_json")
		}
	case "message_delta":
		delta, _ := payload["delta"].(map[string]interface{})
		if reason := strings.TrimSpace(textAgentStringField(delta, "stop_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	case "message_stop":
		p.streamDoneSeen = true
	case "error":
		errValue, _ := payload["error"].(map[string]interface{})
		p.err = errors.New(defaultTextAgentString(textAgentStringField(errValue, "message"), "Claude 上游返回失败"))
	}
}

func (p *TextAgentStreamParser) claudeThinkingBlock(index int, kind string) map[string]interface{} {
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

func (p *TextAgentStreamParser) claudeThinkingBlocks() []interface{} {
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

func (p *TextAgentStreamParser) appendText(delta string) {
	if delta == "" {
		return
	}
	p.text.WriteString(delta)
	if p.options.EmitText != nil {
		p.options.EmitText(delta)
	}
}

func (p *TextAgentStreamParser) appendReasoning(delta string) {
	if delta == "" {
		return
	}
	p.reasoning.WriteString(delta)
	if p.options.EmitReasoning != nil {
		p.options.EmitReasoning(delta)
	}
}

func (p *TextAgentStreamParser) toolCall(index int) *textAgentToolCall {
	if index < 0 {
		index = 0
	}
	if p.toolCalls[index] == nil {
		p.toolCalls[index] = &textAgentToolCall{}
	}
	return p.toolCalls[index]
}

func (p *TextAgentStreamParser) setToolCall(index int, id string, name string, arguments string) {
	call := p.toolCall(index)
	call.id, call.name, call.arguments = id, name, arguments
	if id != "" {
		p.toolCallByID[id] = index
	}
}

// Result validates the terminal facts and returns the existing agent result
// contract as a JSON boundary. Terminally truncated or incomplete tool JSON is
// never accepted.
func (p *TextAgentStreamParser) Result() (json.RawMessage, error) {
	result, err := p.result()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func (p *TextAgentStreamParser) result() (map[string]interface{}, error) {
	if p.err != nil {
		return nil, p.err
	}
	if !p.terminalEventSeen && !p.streamDoneSeen {
		return nil, errors.New("上游流式响应提前中断：未收到结束标记")
	}
	if p.completed != nil {
		return p.completedResult()
	}
	result := map[string]interface{}{"mode": "text", "text": p.text.String(), "toolCalls": []interface{}{}}
	if blocks := p.claudeThinkingBlocks(); len(blocks) > 0 {
		result["reasoningBlocks"] = blocks
	}
	markTextAgentReasoningWire(result, p.wire)
	if p.reasoning.Len() > 0 {
		result["reasoning"] = p.reasoning.String()
	}
	calls, err := p.toolCallResults()
	if err != nil {
		return nil, err
	}
	result["toolCalls"] = calls
	if p.text.Len() == 0 && len(calls) == 0 {
		return nil, errors.New("画布 Agent 接口没有返回内容")
	}
	applyTextAgentStopReason(result, p.stopReason)
	applyTextAgentStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
	return result, nil
}

func (p *TextAgentStreamParser) completedResult() (map[string]interface{}, error) {
	if p.options.ParseCompleted == nil {
		return nil, errors.New("Agent 完成事件解析器未配置")
	}
	payload, err := json.Marshal(p.completed)
	if err != nil {
		return nil, err
	}
	encoded, err := p.options.ParseCompleted(payload)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	if len(encoded) > 0 && string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, fmt.Errorf("Agent 完成事件解析器返回无效 JSON：%w", err)
		}
	}
	if p.text.Len() > 0 {
		result["text"] = p.text.String()
	}
	if p.reasoning.Len() > 0 {
		result["reasoning"] = p.reasoning.String()
	}
	if strings.TrimSpace(p.stopReason) != "" {
		applyTextAgentStopReason(result, p.stopReason)
	}
	applyTextAgentStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
	return result, nil
}

func (p *TextAgentStreamParser) toolCallResults() ([]interface{}, error) {
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
		calls = append(calls, map[string]interface{}{
			"id": call.id, "type": "function",
			"function": map[string]interface{}{"name": call.name, "arguments": arguments},
		})
	}
	return calls, nil
}

func textAgentIntField(value map[string]interface{}, key string, fallback int) int {
	number, ok := value[key].(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return fallback
	}
	return int(number)
}

func textAgentInterfaceSlice(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func textAgentStringField(payload map[string]interface{}, key string) string {
	value, _ := payload[key].(string)
	return value
}

func firstNonEmptyTextAgentString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstRawTextAgentString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultTextAgentString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func textAgentStreamContent(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	parts, ok := value.([]interface{})
	if !ok {
		return ""
	}
	var result strings.Builder
	for _, part := range parts {
		record, _ := part.(map[string]interface{})
		result.WriteString(textAgentStringField(record, "text"))
	}
	return result.String()
}

func responsesStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled", "canceled":
		return true
	}
	return false
}

func responsesStopReason(status string, response map[string]interface{}) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(textAgentStringField(response, "status")))
	}
	if status != "incomplete" {
		return status
	}
	if details, ok := response["incomplete_details"].(map[string]interface{}); ok {
		if reason := strings.TrimSpace(textAgentStringField(details, "reason")); reason != "" {
			return reason
		}
	}
	return "incomplete"
}

func applyTextAgentStopReason(result map[string]interface{}, raw string) {
	if result == nil {
		return
	}
	raw = strings.TrimSpace(raw)
	result["stopReason"] = raw
	result["stopReasonKind"] = normalizeTextAgentStopReason(raw)
}

func applyTextAgentStopFacts(result map[string]interface{}, terminalEventSeen, stopReasonPresent, streamDoneSeen bool) {
	if result == nil {
		return
	}
	result["terminalEventSeen"] = terminalEventSeen
	result["stopReasonPresent"] = stopReasonPresent
	result["streamDoneSeen"] = streamDoneSeen
}

func markTextAgentReasoningWire(result map[string]interface{}, wire string) {
	if result == nil {
		return
	}
	if textAgentStringField(result, "reasoning") != "" || len(textAgentInterfaceSlice(result["reasoningBlocks"])) > 0 {
		result["reasoningWire"] = wire
	}
}

func normalizeTextAgentStopReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "unknown"
	case "stop", "end_turn", "stop_sequence", "completed":
		return "stop"
	case "tool_calls", "function_call", "tool_use":
		return "tool_calls"
	case "length", "max_tokens", "max_output_tokens":
		return "length"
	case "model_context_window_exceeded":
		return "context_limit"
	case "pause_turn":
		return "pause"
	case "refusal":
		return "refusal"
	case "content_filter":
		return "content_filter"
	case "incomplete":
		return "incomplete_unknown"
	default:
		return "unknown"
	}
}
