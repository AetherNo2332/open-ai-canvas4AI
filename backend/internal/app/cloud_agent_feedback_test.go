package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func feedbackResult(t *testing.T, state *cloudAgentRuntime, call cloudAgentCall, result any, err error) map[string]any {
	t.Helper()
	cloudAgentToolResult("feedback-run", state, call, result, err)
	var receipt map[string]any
	message := state.Canonical.Messages[len(state.Canonical.Messages)-1]
	if err := json.Unmarshal([]byte(message["content"].(string)), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestAgentFeedbackSnapshotRepairPreservesOperation(t *testing.T) {
	req := tierAutoRequest()
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	call := piAgentTestCall("stale", "canvas_apply_ops", `{"snapshotHash":"old","ops":[{"type":"update_node","id":"existing","patch":{"title":"Keep this change"}}]}`)
	receipt := feedbackResult(t, state, call, nil, cloudAgentSnapshotConflictError("画布已变化"))
	if receipt["parameters"] != nil || receipt["field"] != "snapshotHash" || receipt["requiredAction"] != "reread_canvas" {
		t.Fatalf("snapshot repair must be compact and actionable: %+v", receipt)
	}
	example, ok := receipt["exampleArguments"].(map[string]any)
	if !ok {
		t.Fatalf("missing snapshot example: %+v", receipt)
	}
	ops := example["ops"].([]any)
	op := ops[0].(map[string]any)
	if op["type"] != "update_node" || op["id"] != "existing" || op["patch"].(map[string]any)["title"] != "Keep this change" || example["snapshotHash"] != "<canvas_get_state.snapshotHash>" {
		t.Fatalf("repair changed the requested operation: %+v", example)
	}
	encoded, _ := json.Marshal(receipt)
	if len(encoded) > 2500 {
		t.Fatalf("snapshot receipt expanded to %d bytes", len(encoded))
	}
}

func TestAgentFeedbackArgumentHintsAndExamples(t *testing.T) {
	req := tierAutoRequest()
	for _, sample := range []struct{ tool, args, field string }{
		{"storyboard", `{}`, "nodeId"},
		{"task_get", `{}`, "taskId"},
		{"storyboard", `{"action":"update","patch":{"durationSeconds":5}}`, "rowId"},
		{"canvas_apply_ops", `{"ops":[{"type":"update_node","id":"private-sentinel","patch":{"x":100}}]}`, "ops[0].id"},
	} {
		t.Run(sample.tool, func(t *testing.T) {
			state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
			receipt := feedbackResult(t, state, piAgentTestCall("bad", sample.tool, sample.args), nil, cloudAgentFieldError(sample.field, "required", "缺少字段"))
			if receipt["parameters"] != nil || receipt["argumentHints"] == nil || receipt["exampleArguments"] == nil {
				t.Fatalf("missing compact repair: %+v", receipt)
			}
			body, _ := json.Marshal(receipt)
			if strings.Contains(string(body), "private-sentinel") {
				t.Fatal("argument repair echoed untrusted values")
			}
			example, _ := json.Marshal(receipt["exampleArguments"])
			if err := validateCloudAgentToolArguments(workflowTool(t, sample.tool), string(example)); err != nil {
				t.Fatalf("repair example violates advertised schema: %s: %v", example, err)
			}
		})
	}
}

func TestAgentFeedbackExamplesKeepSlotsTypesAndFields(t *testing.T) {
	for _, sample := range []struct{ tool, args string }{
		{"storyboard", `{"action":"unbind_output","outputMode":"video"}`},
		{"storyboard", `{"action":"bind_output","outputMode":"video"}`},
		{"canvas_apply_ops", `{"ops":[{"type":"add_node","nodeType":"frame"}]}`},
		{"storyboard", `{"action":"update","patch":{"durationSeconds":12}}`},
		{"canvas_apply_ops", `{"ops":[{"type":"update_node","patch":{"x":120,"y":340}}]}`},
	} {
		t.Run(sample.args, func(t *testing.T) {
			req := tierAutoRequest()
			state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
			receipt := feedbackResult(t, state, piAgentTestCall("bad", sample.tool, sample.args), nil, cloudAgentFieldError("id", "required", "缺少ID"))
			example, ok := receipt["exampleArguments"].(map[string]any)
			if !ok {
				t.Fatalf("missing example: %+v", receipt)
			}
			var original map[string]any
			_ = json.Unmarshal([]byte(sample.args), &original)
			if sample.tool == "canvas_apply_ops" {
				original, example = creationMaps(original["ops"])[0], creationMaps(example["ops"])[0]
			}
			for _, key := range []string{"outputMode", "nodeType", "patch"} {
				if expected, ok := original[key]; ok && !reflect.DeepEqual(example[key], expected) {
					t.Fatalf("example changed %s: expected %+v, got %+v", key, expected, example[key])
				}
			}
		})
	}
	for _, sample := range []struct{ tool, args string }{
		{"storyboard", `{"action":"update"}`},
		{"storyboard", `{"action":"unbind_output"}`},
		{"canvas_apply_ops", `{"ops":[{"type":"add_node"}]}`},
		{"canvas_apply_ops", `{"ops":[{"type":"update_node","patch":{"x":"100"}}]}`},
		{"storyboard", `{"action":"update","patch":{"durationSeconds":"5"}}`},
	} {
		req := tierAutoRequest()
		state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
		receipt := feedbackResult(t, state, piAgentTestCall("unknown", sample.tool, sample.args), nil, cloudAgentFieldError("arguments", "required", "缺少语义字段"))
		if receipt["exampleArguments"] != nil || receipt["argumentHints"] == nil {
			t.Fatalf("ambiguous write must return hints without an invented example: %+v", receipt)
		}
	}
}

func TestAgentFeedbackStoryboardInvalidRowsAreReportedAtomically(t *testing.T) {
	rows := []map[string]any{{"id": "r1", "dialogue": "keep"}, {"id": "r2", "dialogue": "keep"}}
	before, _ := json.Marshal(rows)
	_, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, cloudAgentStoryboardEditArgs{
		Action: "update", RowIDs: []string{"r1", "bad-a", "r1", "bad-b", "bad-a"}, Patch: map[string]any{"dialogue": "replace"},
	}, "user", "call")
	if err == nil {
		t.Fatal("invalid batch accepted")
	}
	req := tierAutoRequest()
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	receipt := feedbackResult(t, state, piAgentTestCall("rows", "storyboard", `{"snapshotHash":"h","nodeId":"board","action":"update","rowIds":["r1","bad-a","r1","bad-b","bad-a"],"patch":{"dialogue":"replace"}}`), nil, err)
	if !reflect.DeepEqual(receipt["invalidRowIds"], []any{"bad-a", "bad-b"}) || !reflect.DeepEqual(receipt["duplicateRowIds"], []any{"r1", "bad-a"}) || receipt["errorClass"] != "schema_error" {
		t.Fatalf("lost row diagnostics: %+v", receipt)
	}
	after, _ := json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("invalid batch partially changed rows")
	}
}

func TestAgentFeedbackReadBudgetIsCurrentAndDoesNotMutateResult(t *testing.T) {
	original := map[string]any{"rows": []any{}, "readCallsRemaining": 99}
	for _, sample := range []struct{ used, limit, remaining int }{{7, 10, 3}, {10, 10, 0}, {11, 10, 0}} {
		state := &cloudAgentRuntime{ReadToolCalls: sample.used, CallLimits: cloudAgentCallLimits{ReadCallsPerRun: sample.limit}}
		receipt := feedbackResult(t, state, piAgentTestCall("read", "storyboard", `{}`), original, nil)
		if receipt["readCallsUsed"] != float64(sample.used) || receipt["readCallsLimit"] != float64(sample.limit) || receipt["readCallsRemaining"] != float64(sample.remaining) {
			t.Fatalf("wrong remaining budget: %+v", receipt)
		}
	}
	if original["readCallsRemaining"] != 99 {
		t.Fatal("receipt modified cached result")
	}
	state := &cloudAgentRuntime{ReadToolCalls: 10, CallLimits: cloudAgentCallLimits{ReadCallsPerRun: 10}}
	receipt := feedbackResult(t, state, piAgentTestCall("read", "task_get", `{}`), nil, errors.New("read rejected"))
	if receipt["readCallsRemaining"] != float64(0) {
		t.Fatal("failed read omitted remaining quota")
	}
}

func TestAgentFeedbackNodeStyleErrorNamesPatch(t *testing.T) {
	raw := `{"snapshotHash":"h","ops":[{"type":"add_node","id":"n","nodeType":"frame","width":600}]}`
	for _, check := range []struct {
		name string
		run  func() error
	}{
		{"schema", func() error { return validateCloudAgentToolArguments(workflowTool(t, "canvas_apply_ops"), raw) }},
		{"decoder", func() error { _, err := decodeCloudAgentCanvasArgs(raw); return err }},
	} {
		t.Run(check.name, func(t *testing.T) {
			err := check.run()
			var field *cloudAgentFieldArgumentError
			if !errors.As(err, &field) || field.Field != "ops[0].width" || !strings.Contains(err.Error(), "update_node.patch") {
				t.Fatalf("style error must identify the field and its supported location: %v", err)
			}
		})
	}
}

func TestAgentFeedbackCachedReadKeepsRowDiagnostics(t *testing.T) {
	req := tierAutoRequest()
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	call := piAgentTestCall("cached", "storyboard", `{"nodeId":"board","rowIds":["missing","r1","r1"]}`)
	var cached cloudAgentCachedToolResult
	if err := json.Unmarshal([]byte(`{"error":"分镜行不存在或重复","argumentError":true,"field":"rowIds","issue":"invalid_row_ids","invalidRowIds":["missing"],"duplicateRowIds":["r1"]}`), &cached); err != nil {
		t.Fatal(err)
	}
	state.ToolReadResults = map[string]cloudAgentCachedToolResult{cloudAgentReadCacheKeyForState(nil, "user", state, call): cached}
	result, err := cloudAgentReadToolCached(nil, "user", state, call)
	receipt := feedbackResult(t, state, call, result, err)
	if !reflect.DeepEqual(receipt["invalidRowIds"], []any{"missing"}) || !reflect.DeepEqual(receipt["duplicateRowIds"], []any{"r1"}) || receipt["field"] != "rowIds" || state.ReadToolCalls != 0 {
		t.Fatalf("cached argument error lost diagnostics or consumed quota: %+v", receipt)
	}
}
