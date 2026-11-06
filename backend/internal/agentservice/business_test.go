package agentservice

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeAdapter struct {
	app   string
	tools []ToolSpec
}

func (a fakeAdapter) Application() string  { return a.app }
func (a fakeAdapter) SpaceTypes() []string { return []string{"test_space"} }
func (a fakeAdapter) ValidateSpace(context.Context, string, Scope) error {
	return nil
}
func (a fakeAdapter) Tools() []ToolSpec { return a.tools }
func (a fakeAdapter) Execute(context.Context, ToolInvocation) (ToolResult, error) {
	return ToolResult{Text: "ok"}, nil
}

func TestAdapterRegistryOwnsApplicationsAndTools(t *testing.T) {
	registry := NewAdapterRegistry()
	adapter := fakeAdapter{app: "test", tools: []ToolSpec{{Name: "test_read", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter); err == nil {
		t.Fatal("second adapter for one application accepted")
	}
	if err := registry.Register(fakeAdapter{app: "other", tools: []ToolSpec{{Name: "test_read"}}}); err == nil {
		t.Fatal("tool name owned by another application accepted")
	}
	if err := registry.Register(fakeAdapter{app: "bad", tools: []ToolSpec{{Name: "has space"}}}); err == nil {
		t.Fatal("invalid tool name accepted")
	}
	got, ok := registry.Adapter("test")
	if !ok || got.Application() != "test" {
		t.Fatal("registered adapter not found")
	}
	if _, ok := registry.Adapter("missing"); ok {
		t.Fatal("unknown application resolved")
	}
	if tool, ok := registry.Tool("test", "test_read"); !ok || tool.Name != "test_read" {
		t.Fatal("tool lookup failed")
	}
	if _, ok := registry.Tool("other", "test_read"); ok {
		t.Fatal("tool resolved under the wrong application")
	}
}

func TestBuildContextKeepsToolCallsAndResults(t *testing.T) {
	in := contextTestInput()
	in.Messages = append(in.Messages,
		HistoryMessage{ID: "c1", OwnerID: "u1", SessionID: "s1", Sequence: 4, Kind: MessageKindToolCall, Actor: ActorExecutor, Role: "assistant", Text: `[{"id":"call-1","name":"drama.read","arguments":{}}]`},
		HistoryMessage{ID: "r1", OwnerID: "u1", SessionID: "s1", Sequence: 5, Kind: MessageKindToolResult, Actor: ActorSystem, Role: "tool", Text: `{"toolCallId":"call-1","text":"读到了"}`},
	)
	snapshot, err := BuildContext(in)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, block := range snapshot.Blocks {
		kinds = append(kinds, block.Kind)
	}
	if kinds[len(kinds)-2] != BlockToolCall || kinds[len(kinds)-1] != BlockToolResult {
		t.Fatalf("tool blocks missing: %v", kinds)
	}
	if text := RenderText(snapshot); text == "" || contains(text, "读到了") {
		t.Fatalf("tool results must stay out of the system text: %q", text)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
