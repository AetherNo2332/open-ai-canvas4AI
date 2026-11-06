package creationplatform

import (
	"encoding/json"
	"testing"
)

func structuredTestRequest() StructuredTextRequest {
	return StructuredTextRequest{
		Model:  ModelSelection{ChannelID: "c1", ModelKey: "m1"},
		System: "你是短剧助手",
		Messages: []StructuredMessage{
			{Role: RoleUser, Text: "写第一集"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "drama.read", Arguments: json.RawMessage(`{"episode":1}`)}}},
			{Role: RoleTool, ToolCallID: "call-1", Text: `{"title":"开端"}`},
		},
		Tools:          []ToolDefinition{{Name: "drama.read", Description: "读取剧集", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:     ToolChoiceAuto,
		Business:       BusinessRef{Application: "drama", ObjectType: "agent_step", ObjectID: "s1"},
		IdempotencyKey: "step-1",
	}
}

func TestStructuredTextRequestKeepsToolSemantics(t *testing.T) {
	if err := structuredTestRequest().Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := map[string]func(*StructuredTextRequest){
		"tool result without call":   func(r *StructuredTextRequest) { r.Messages[2].ToolCallID = "call-9" },
		"duplicate call id":          func(r *StructuredTextRequest) { r.Messages = append(r.Messages, r.Messages[1]) },
		"call without name":          func(r *StructuredTextRequest) { r.Messages[1].ToolCalls[0].Name = "" },
		"invalid arguments":          func(r *StructuredTextRequest) { r.Messages[1].ToolCalls[0].Arguments = json.RawMessage(`{`) },
		"empty assistant":            func(r *StructuredTextRequest) { r.Messages[1].ToolCalls = nil },
		"user with tool calls":       func(r *StructuredTextRequest) { r.Messages[0].ToolCalls = r.Messages[1].ToolCalls },
		"unknown role":               func(r *StructuredTextRequest) { r.Messages[0].Role = "developer" },
		"duplicate tool":             func(r *StructuredTextRequest) { r.Tools = append(r.Tools, r.Tools[0]) },
		"required without tools":     func(r *StructuredTextRequest) { r.Tools, r.ToolChoice = nil, ToolChoiceRequired },
		"unknown tool choice":        func(r *StructuredTextRequest) { r.ToolChoice = "any" },
		"no messages":                func(r *StructuredTextRequest) { r.Messages = nil },
		"no idempotency key":         func(r *StructuredTextRequest) { r.IdempotencyKey = " " },
		"no business":                func(r *StructuredTextRequest) { r.Business.Application = "" },
		"logical and system at once": func(r *StructuredTextRequest) { r.Model.LogicalModelID = "lm" },
		"half system selection":      func(r *StructuredTextRequest) { r.Model.ModelKey = "" },
	}
	for name, mutate := range cases {
		req := structuredTestRequest()
		mutate(&req)
		if err := req.Validate(); !IsCode(err, CodeInvalidRequest) {
			t.Fatalf("%s: want invalid_request, got %v", name, err)
		}
	}
}

func TestStructuredFingerprintTracksContentNotTrace(t *testing.T) {
	a := structuredTestRequest()
	b := structuredTestRequest()
	b.TraceID = "trace-2"
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("trace ID must not change the fingerprint")
	}
	c := structuredTestRequest()
	c.Messages[2].Text = `{"title":"变了"}`
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatal("a different tool result must change the fingerprint")
	}
}
