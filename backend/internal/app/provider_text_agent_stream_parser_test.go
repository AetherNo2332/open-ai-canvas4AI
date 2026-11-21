package app

import (
	"errors"
	"strings"
	"testing"
)

func TestStreamingAgentParserKeepsHostErrorClassificationAndReasoningCallback(t *testing.T) {
	parser := newStreamingAgentParser("chat-completion", nil)
	parser.consume("text/event-stream", []byte(`data: {"error":{"message":"insufficient balance"}}`+"\n\n"))
	if _, err := parser.result(); err == nil {
		t.Fatal("payload error was ignored")
	} else {
		var payloadErr providerPayloadError
		if !errors.As(err, &payloadErr) {
			t.Fatalf("payload error type = %T, want providerPayloadError", err)
		}
	}

	var reasoning strings.Builder
	parser = newStreamingAgentParser("chat-completion", nil)
	parser.emitReasoning = func(delta string) { reasoning.WriteString(delta) }
	parser.consume("text/event-stream", []byte(`data: {"choices":[{"delta":{"reasoning_content":"step "}}]}`+"\n\n"))
	parser.consume("text/event-stream", []byte(`data: {"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`+"\n\n"))
	result, err := parser.result()
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.String() != "step" || result["reasoning"] != "step" || result["text"] != "answer" {
		t.Fatalf("reasoning callback/result = %q, %#v", reasoning.String(), result)
	}
}
