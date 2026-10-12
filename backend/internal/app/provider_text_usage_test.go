package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/protocol"
)

func TestProviderPiUsageFromBody(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		want     map[string]any
	}{
		{
			name:     "chat completion stream splits reported cached input",
			protocol: "chat-completion",
			body:     "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":23,\"completion_tokens\":7,\"total_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":5}}}\n\ndata: [DONE]\n\n",
			want:     map[string]any{"input": int64(18), "output": int64(7), "cacheRead": int64(5), "totalTokens": int64(30)},
		},
		{
			name:     "responses stream reads nested usage",
			protocol: "responses",
			body:     "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":31,\"output_tokens\":11,\"total_tokens\":42,\"input_tokens_details\":{\"cached_tokens\":9}}}}\n\n",
			want:     map[string]any{"input": int64(22), "output": int64(11), "cacheRead": int64(9), "totalTokens": int64(42)},
		},
		{
			name:     "claude stream preserves reported cache fields",
			protocol: "claude-api",
			body:     "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":31,\"output_tokens\":0,\"cache_read_input_tokens\":9,\"cache_creation_input_tokens\":2}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":11}}\n\n",
			want:     map[string]any{"input": int64(31), "output": int64(11), "cacheRead": int64(9), "cacheWrite": int64(2)},
		},
		{
			name:     "gemini usage metadata",
			protocol: "gemini",
			body:     `{"usageMetadata":{"promptTokenCount":22,"candidatesTokenCount":4,"cachedContentTokenCount":8,"totalTokenCount":26}}`,
			want:     map[string]any{"input": int64(14), "output": int64(4), "cacheRead": int64(8), "totalTokens": int64(26)},
		},
		{
			name:     "partial usage keeps unreported counts absent",
			protocol: "chat-completion",
			body:     `{"usage":{"prompt_tokens":8}}`,
			want:     map[string]any{"input": int64(8)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := providerPiUsageFromBody([]byte(test.body), test.protocol)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("providerPiUsageFromBody() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestProviderPiUsageMissingRemainsUnknown(t *testing.T) {
	if got := providerPiUsageFromBody([]byte(`{"choices":[{"message":{"content":"ok"}}]}`), "chat-completion"); got != nil {
		t.Fatalf("provider usage without usage fields = %#v, want nil", got)
	}
	if got := providerPiUsageFromManifestUsage(map[string]any{}); got != nil {
		t.Fatalf("empty manifest usage = %#v, want nil", got)
	}
}

func TestProviderTextTaskResultCarriesOnlyKnownUsage(t *testing.T) {
	result := providerTextTaskResult(providerTextResult{
		Text:  "answer",
		Usage: map[string]any{"input": int64(13), "output": int64(4), "totalTokens": int64(17)},
	})
	if got := result["usage"]; !reflect.DeepEqual(got, map[string]any{"input": int64(13), "output": int64(4), "totalTokens": int64(17)}) {
		t.Fatalf("task result usage = %#v", got)
	}
	if _, exists := result["cacheRead"]; exists {
		t.Fatal("usage metrics must remain nested under usage")
	}
	missing := providerTextTaskResult(providerTextResult{Text: "answer"})
	if _, exists := missing["usage"]; exists {
		t.Fatalf("task result fabricated unknown usage: %#v", missing["usage"])
	}
}

func TestFinishProtocolTextResultCarriesReportedUsage(t *testing.T) {
	result, err := finishProtocolResult(context.Background(), providerConfig{InterfaceType: string(model.ChannelInterfaceChatCompletion)}, "text", "task", &protocol.Result{
		Text: "answer", Usage: map[string]any{"prompt_tokens": int64(10), "completion_tokens": int64(4), "total_tokens": int64(14)},
	}, defaultVideoPollPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := result["usage"]; !reflect.DeepEqual(got, map[string]any{"input": int64(10), "output": int64(4), "totalTokens": int64(14)}) {
		t.Fatalf("finished task result usage = %#v", got)
	}
}

func TestProviderUsageResultJSONOmitsMissingUsage(t *testing.T) {
	encoded, err := json.Marshal(providerTextTaskResult(providerTextResult{Text: "answer"}))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"mode":"text","text":"answer"}` {
		t.Fatalf("task JSON = %s, want no usage field", encoded)
	}
}
