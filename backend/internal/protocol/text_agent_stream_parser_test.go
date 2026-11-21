package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func feedTextAgentFrames(parser *TextAgentStreamParser, frames ...string) {
	for _, frame := range frames {
		parser.Consume("text/event-stream", []byte(frame+"\n\n"))
	}
	parser.Flush()
}

func TestTextAgentStreamParserChatPreservesUnknownFieldsAndToolCalls(t *testing.T) {
	var emitted strings.Builder
	parser := NewTextAgentStreamParser(TextAgentStreamOptions{
		Wire:     TextWireChatCompletion,
		EmitText: func(delta string) { emitted.WriteString(delta) },
	})
	feedTextAgentFrames(parser,
		`data: {"choices":[{"delta":{"content":[{"type":"text","text":"你"},{"type":"text","text":"好"}],"reasoning_content":"思考"},"future_choice":true}],"future_top":{"ignored":true}}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"sea","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"rch","arguments":"\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	result, err := parser.result()
	if err != nil {
		t.Fatal(err)
	}
	if result["text"] != "你好" || result["reasoning"] != "思考" || emitted.String() != "你好" {
		t.Fatalf("text result = %#v, emitted = %q", result, emitted.String())
	}
	calls := result["toolCalls"].([]interface{})
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v", calls)
	}
	call := calls[0].(map[string]interface{})
	function := call["function"].(map[string]interface{})
	if call["id"] != "call-1" || function["name"] != "search" || function["arguments"] != `{"q":"x"}` {
		t.Fatalf("tool call = %#v", call)
	}
	if result["stopReason"] != "tool_calls" || result["stopReasonKind"] != "tool_calls" ||
		result["terminalEventSeen"] != true || result["streamDoneSeen"] != true {
		t.Fatalf("terminal facts = %#v", result)
	}
}

func TestTextAgentStreamParserRejectsTruncatedAndIncompleteStreams(t *testing.T) {
	t.Run("missing terminal marker", func(t *testing.T) {
		parser := NewTextAgentStreamParser(TextAgentStreamOptions{Wire: TextWireChatCompletion})
		feedTextAgentFrames(parser, `data: {"choices":[{"delta":{"content":"half"}}]}`)
		if _, err := parser.result(); err == nil || !strings.Contains(err.Error(), "提前中断") {
			t.Fatalf("truncated stream error = %v", err)
		}
	})
	t.Run("truncated tool JSON", func(t *testing.T) {
		parser := NewTextAgentStreamParser(TextAgentStreamOptions{Wire: TextWireChatCompletion})
		feedTextAgentFrames(parser,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"search","arguments":"{\"q\":"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		)
		if _, err := parser.result(); err == nil || !strings.Contains(err.Error(), "工具参数不是完整 JSON") {
			t.Fatalf("truncated tool JSON error = %v", err)
		}
	})
	t.Run("failed response", func(t *testing.T) {
		parser := NewTextAgentStreamParser(TextAgentStreamOptions{Wire: TextWireResponses})
		feedTextAgentFrames(parser, `data: {"type":"response.failed","response":{"status":"failed"}}`)
		if _, err := parser.result(); err == nil || !strings.Contains(err.Error(), "failed") {
			t.Fatalf("failed response error = %v", err)
		}
	})
	t.Run("invalid event JSON", func(t *testing.T) {
		parser := NewTextAgentStreamParser(TextAgentStreamOptions{Wire: TextWireChatCompletion})
		feedTextAgentFrames(parser, `data: {not-json}`)
		if _, err := parser.result(); err == nil || !strings.Contains(err.Error(), "Agent 流式事件解析失败") {
			t.Fatalf("invalid event error = %v", err)
		}
	})
}

func TestTextAgentStreamParserResponsesCompletedUsesAdapter(t *testing.T) {
	parser := NewTextAgentStreamParser(TextAgentStreamOptions{
		Wire: TextWireResponses,
		ParseCompleted: func(raw json.RawMessage) (json.RawMessage, error) {
			var payload map[string]interface{}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["status"] != "completed" {
				t.Fatalf("completed payload = %#v", payload)
			}
			return json.Marshal(map[string]interface{}{"mode": "text", "text": "snapshot", "toolCalls": []interface{}{}})
		},
	})
	feedTextAgentFrames(parser,
		`data: {"type":"response.output_text.delta","delta":"streamed "}`,
		`data: {"type":"response.completed","response":{"status":"completed","future_field":true}}`,
	)
	result, err := parser.result()
	if err != nil {
		t.Fatal(err)
	}
	if result["text"] != "streamed " {
		t.Fatalf("completed text = %#v", result)
	}
	if result["stopReason"] != "completed" || result["stopReasonKind"] != "stop" ||
		result["terminalEventSeen"] != true || result["streamDoneSeen"] != true {
		t.Fatalf("completed facts = %#v", result)
	}
}

func TestTextAgentStreamParserClaudePreservesThinkingBlocks(t *testing.T) {
	parser := NewTextAgentStreamParser(TextAgentStreamOptions{Wire: TextWireClaude})
	feedTextAgentFrames(parser,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"想","signature":"sig"}}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":"答"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"法"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"案"}}`,
		`data: {"type":"message_stop"}`,
	)
	result, err := parser.result()
	if err != nil {
		t.Fatal(err)
	}
	if result["text"] != "答案" || result["reasoning"] != "想法" || result["reasoningWire"] != TextWireClaude {
		t.Fatalf("Claude result = %#v", result)
	}
	blocks := result["reasoningBlocks"].([]interface{})
	thinking := blocks[0].(map[string]interface{})
	if thinking["thinking"] != "想法" || thinking["signature"] != "sig" {
		t.Fatalf("thinking block = %#v", thinking)
	}
	if result["terminalEventSeen"] != false || result["streamDoneSeen"] != true || result["stopReasonKind"] != "unknown" {
		t.Fatalf("Claude facts = %#v", result)
	}
}
