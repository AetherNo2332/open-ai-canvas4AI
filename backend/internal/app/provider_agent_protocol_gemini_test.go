package app

import "testing"

func TestGeminiNestedToolHistory(t *testing.T) {
	source := nestedAgentHistory(`{"nodeIds":["node-1"]}`)
	calls := source.Messages[1]["tool_calls"].([]map[string]interface{})
	calls[0]["thoughtSignature"] = "test-signature"
	source.Messages[1]["content"] = "Inspecting canvas"
	wire := serializedAgentBody(t, canonicalAgentGeminiBody(source))
	contents := wire["contents"].([]interface{})
	parts := contents[1].(map[string]interface{})["parts"].([]interface{})
	if len(parts) != 2 || parts[0].(map[string]interface{})["text"] != "Inspecting canvas" {
		t.Fatalf("lost text/call: %#v", parts)
	}
	part := parts[1].(map[string]interface{})
	call := part["functionCall"].(map[string]interface{})
	if call["name"] != "canvas_get_state" || part["thoughtSignature"] != "test-signature" {
		t.Fatalf("lost call identity/signature: %#v", part)
	}
	response := contents[2].(map[string]interface{})["parts"].([]interface{})[0].(map[string]interface{})["functionResponse"].(map[string]interface{})
	if response["name"] != call["name"] || response["id"] != call["id"] {
		t.Fatalf("wrong result pairing: %#v", response)
	}
}
