package app

import (
	"errors"
	"strings"
	"testing"
)

// 参数错误要把本轮实际暴露的 schema 回给模型，否则它只会重复同一个错参数
// （实测 canvas_get_state 被塞过 canvasId / limit，反复重试直到被 nudge 打断）。
func TestCloudAgentArgumentErrorCarriesAdvertisedSchema(t *testing.T) {
	state := &cloudAgentRuntime{
		Canonical: canonicalAgentRequest{Tools: []map[string]any{
			{"type": "function", "function": map[string]any{"name": "canvas_get_state", "parameters": map[string]any{"type": "object", "properties": map[string]any{"offset": map[string]any{"type": "integer"}}}}},
			{"type": "function", "function": map[string]any{"name": "plan_update", "parameters": map[string]any{"type": "object"}}},
		}},
		Events: []CloudAgentEvent{},
	}
	call := cloudAgentCall{ID: "call-1"}
	call.Function.Name = "canvas_get_state"
	call.Function.Arguments = `{"canvasId":"c"}`
	cloudAgentToolResult("run-1", state, call, nil, cloudAgentJSONArgumentError(errors.New("unknown field")))

	var payload map[string]any
	for _, event := range state.Events {
		if event.Type == "tool_failed" {
			payload = event.Payload
		}
	}
	if payload == nil {
		t.Fatal("参数错误应当记成 tool_failed 事件")
	}
	detail, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("工具失败回执结构不对: %+v", payload)
	}
	if detail["reason"] != "invalid_tool_arguments" {
		t.Fatalf("缺少可识别的失败原因: %+v", detail)
	}
	parameters, ok := detail["parameters"].(map[string]any)
	if !ok || parameters["type"] != "object" {
		t.Fatalf("没有把该工具的 parameters 回给模型: %+v", detail["parameters"])
	}
	if !strings.Contains(stringValue(detail["guidance"]), "不要重复提交相同的错误参数") {
		t.Fatalf("缺少自纠错提示: %+v", detail["guidance"])
	}
	if example, ok := detail["exampleArguments"].(map[string]any); !ok || len(example) != 0 {
		t.Fatalf("canvas_get_state 应给出空对象示例: %+v", detail["exampleArguments"])
	}

	// 非参数类错误不附带 schema（避免把回执撑大、也避免误导模型去改参数）。
	other := &cloudAgentRuntime{Canonical: state.Canonical, Events: []CloudAgentEvent{}}
	otherCall := cloudAgentCall{ID: "call-2"}
	otherCall.Function.Name = "plan_update"
	otherCall.Function.Arguments = `{}`
	cloudAgentToolResult("run-1", other, otherCall, nil, errors.New("上游拒绝"))
	for _, event := range other.Events {
		if event.Type != "tool_failed" {
			continue
		}
		detail, _ := event.Payload["result"].(map[string]any)
		if _, exists := detail["parameters"]; exists {
			t.Fatalf("非参数错误不该附带 schema: %+v", detail)
		}
		if _, exists := detail["reason"]; exists {
			t.Fatalf("非参数错误不该标 invalid_tool_arguments: %+v", detail)
		}
	}
}
