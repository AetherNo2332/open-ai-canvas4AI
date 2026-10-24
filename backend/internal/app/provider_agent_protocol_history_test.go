package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func nestedAgentHistory(arguments string) *canonicalAgentRequest {
	return &canonicalAgentRequest{
		Messages: []map[string]interface{}{
			{"role": "user", "content": "Read the current canvas."},
			{"role": "assistant", "content": "", "tool_calls": []map[string]interface{}{{
				"id": "call-invalid-history", "function": map[string]interface{}{"name": "canvas_get_state", "arguments": arguments},
			}}},
			{"role": "tool", "tool_call_id": "call-invalid-history", "content": `{"error":"invalid_tool_arguments","retryable":true,"taskSubmitted":false}`},
		},
		Tools: []map[string]interface{}{{"type": "function", "function": map[string]interface{}{
			"name": "canvas_get_state", "parameters": map[string]interface{}{"type": "object"},
		}}},
		ToolChoice: "auto",
	}
}

func serializedAgentBody(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExpandCanonicalAgentNestedClaudeHistoryProjectsObjectInputs(t *testing.T) {
	cases := []struct {
		name, arguments string
		want            map[string]interface{}
	}{
		{"malformed", `{"nodeIds":[`, map[string]interface{}{}},
		{"empty", "", map[string]interface{}{}},
		{"whitespace", " \n\t", map[string]interface{}{}},
		{"null", "null", map[string]interface{}{}},
		{"array", `[{"nodeId":"a"}]`, map[string]interface{}{}},
		{"empty_array", `[]`, map[string]interface{}{}},
		{"string", `"not an object"`, map[string]interface{}{}},
		{"number", "42", map[string]interface{}{}},
		{"boolean", "false", map[string]interface{}{}},
		{"object", ` {"nodeIds":["a"],"include":{"connections":false}} `, map[string]interface{}{"nodeIds": []interface{}{"a"}, "include": map[string]interface{}{"connections": false}}},
	}
	for _, declarative := range []bool{false, true} {
		mode := "builtin"
		if declarative {
			mode = "declarative"
		}
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				source := nestedAgentHistory(tc.arguments)
				before, _ := json.Marshal(source)
				expanded, err := expandCanonicalAgentRequest(source, providerConfig{Model: "claude-history", InterfaceType: string(model.ChannelInterfaceClaudeAPI)}, declarative)
				if err != nil {
					t.Fatal(err)
				}
				wire := serializedAgentBody(t, expanded.Claude)
				messages := wire["messages"].([]interface{})
				assistant := messages[1].(map[string]interface{})
				call := assistant["content"].([]interface{})[0].(map[string]interface{})
				input, object := call["input"].(map[string]interface{})
				if !object || !reflect.DeepEqual(input, tc.want) {
					t.Fatalf("Claude history tool input = %#v (%T), want object %#v", call["input"], call["input"], tc.want)
				}
				if assistant["role"] != "assistant" || call["type"] != "tool_use" || call["id"] != "call-invalid-history" || call["name"] != "canvas_get_state" {
					t.Fatalf("Claude history lost call identity: %#v", assistant)
				}
				result := messages[2].(map[string]interface{})["content"].([]interface{})[0].(map[string]interface{})
				if result["type"] != "tool_result" || result["tool_use_id"] != "call-invalid-history" || result["content"] != source.Messages[2]["content"] {
					t.Fatalf("Claude history lost the tool error result: %#v", result)
				}
				after, _ := json.Marshal(source)
				if string(before) != string(after) {
					t.Fatalf("materializing Claude history changed the canonical raw arguments")
				}
			})
		}
	}
}

func TestExpandCanonicalAgentNestedHistoryPreservesOpenAIRawArguments(t *testing.T) {
	for _, arguments := range []string{`{"nodeIds":[`, "", "null", `[]`, "42", ` {"nodeIds":["a"]} `} {
		for _, declarative := range []bool{false, true} {
			for _, responses := range []bool{false, true} {
				config := providerConfig{}
				if responses {
					config.InterfaceType = string(model.ChannelInterfaceOpenAIResponse)
				}
				source := nestedAgentHistory(arguments)
				expanded, err := expandCanonicalAgentRequest(source, config, declarative)
				if err != nil {
					t.Fatal(err)
				}
				var call map[string]interface{}
				if responses {
					wire := serializedAgentBody(t, expanded.Responses)
					call = wire["input"].([]interface{})[2].(map[string]interface{})
				} else {
					wire := serializedAgentBody(t, expanded.ChatCompletion)
					message := wire["messages"].([]interface{})[1].(map[string]interface{})
					call = message["tool_calls"].([]interface{})[0].(map[string]interface{})["function"].(map[string]interface{})
				}
				if call["arguments"] != arguments {
					t.Fatalf("OpenAI projection changed raw arguments: declarative=%t responses=%t got=%#v want=%q", declarative, responses, call["arguments"], arguments)
				}
			}
		}
	}
}
