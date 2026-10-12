package app

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

// piToolBatchBody is the exact body agent/src/bridge.ts sends for
// POST /internal-agent/runs/:id/tool-batches.
//
// It must stay byte-compatible with the Node producer:
//
//	bridge.startToolBatch → { taskId, calls: PiToolCall[] }
//	PiToolCall = { id, type: "function", function: { name, arguments }, thoughtSignature? }
//	callsFromAssistant() always sets type: "function".
//
// The route decodes with DisallowUnknownFields, so any field the producer sends
// but the Go struct does not declare rejects the WHOLE batch with an empty 400.
const piToolBatchBody = `{"taskId":"task-wire","calls":[{"id":"call_1","type":"function",` +
	`"function":{"name":"canvas_get_state","arguments":"{}"}}]}`

// decodeStrict mirrors the decoder configuration of
// backend/internal/handler/internal_agent.go for /tool-batches: MaxBytesReader is
// not part of this test, but DisallowUnknownFields and the trailing-token check are.
func decodeStrict(t *testing.T, body string, target any) error {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}

// TestPiToolBatchAcceptsTheNodeProducerShape pins the internal wire contract for
// tool batches.
//
// Regression: `cloudAgentCall` declared only id/function, so the `type` marker the
// Node producer always sends was "unknown" and every batch was rejected with HTTP
// 400 in ~50µs (empty body). The worker retried forever while still renewing its
// lease, so neither lease expiry nor the stalled-run watchdog could terminate the
// run — it stayed in `running` indefinitely and the canvas Agent never completed.
func TestPiToolBatchAcceptsTheNodeProducerShape(t *testing.T) {
	var request PiToolBatchRequest
	if err := decodeStrict(t, piToolBatchBody, &request); err != nil {
		t.Fatalf("the Node tool-batch body was rejected by the strict decoder: %v", err)
	}
	if len(request.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(request.Calls))
	}
	call := request.Calls[0]
	if call.ID != "call_1" {
		t.Fatalf("id = %q", call.ID)
	}
	if call.Type != "function" {
		t.Fatalf("type = %q, want function", call.Type)
	}
	if call.Function.Name != "canvas_get_state" {
		t.Fatalf("function.name = %q", call.Function.Name)
	}
	if call.Function.Arguments == "" {
		t.Fatal("function.arguments must survive decoding")
	}
}

// TestPiToolBatchAcceptsThoughtSignature: some upstreams return a reasoning
// signature on the tool call; the producer forwards it, so the contract must too.
func TestPiToolBatchAcceptsThoughtSignature(t *testing.T) {
	body := `{"taskId":"task-wire","calls":[{"id":"call_2","type":"function","thoughtSignature":"sig-abc",` +
		`"function":{"name":"canvas_get_state","arguments":"{}"}}]}`
	var request PiToolBatchRequest
	if err := decodeStrict(t, body, &request); err != nil {
		t.Fatalf("thoughtSignature was rejected by the strict decoder: %v", err)
	}
	if request.Calls[0].ThoughtSignature != "sig-abc" {
		t.Fatalf("thoughtSignature = %q", request.Calls[0].ThoughtSignature)
	}
}
