package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestCloudAgentBatchSelectionCredential(t *testing.T) {
	s, _ := agentMediaBatchFixture(t)
	token, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "seedance-test"})
	if err != nil {
		t.Fatal(err)
	}
	args := agentMediaBatchArgs("v1")
	delete(args, "channelId")
	delete(args, "channelModelKey")
	args["selectionId"] = token
	run, state := agentMediaBatchRun(t, s, args, 4)
	call := state.Calls[0]
	call.Function.Name = "canvas_preflight"
	delete(args, "concurrency")
	delete(args, "maxRetriesPerItem")
	raw, _ := json.Marshal(args)
	call.Function.Arguments = string(raw)
	for _, tool := range []cloudAgentCall{call, state.Calls[0]} {
		schema, ok := cloudAgentAdvertisedTool(&state, tool.Function.Name)
		if !ok {
			t.Fatalf("tool not advertised: %s", tool.Function.Name)
		}
		if err := validateCloudAgentToolArguments(schema, tool.Function.Arguments); err != nil {
			t.Errorf("%s should accept selectionId: %v", tool.Function.Name, err)
		}
	}
	result, err := cloudAgentHandleCanvasPreflight(s.repo, "user", &state, call, s)
	if err != nil {
		t.Fatal(err)
	}
	if report := result.(map[string]any); report["ok"] != true || report["estimatedCredits"] == nil {
		t.Fatalf("credential did not reach model preflight: %#v", report)
	}
	if err := s.requestCloudAgentMediaBatchApproval(run, &state, state.Calls[0]); err != nil || state.MediaBatch == nil {
		t.Fatalf("credential did not reach batch approval: %v", err)
	}
}

func TestCloudAgentBatchSelectionRejectsInvalidCredentials(t *testing.T) {
	s, _ := agentMediaBatchFixture(t)
	token, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "seedance-test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ user, credential, legacy, issue string }{
		{"user", token, "model", "mutually_exclusive"},
		{"other-user", token, "", "invalid_value"},
		{"user", token + "x", "", "invalid_value"},
		{"user", "invalid", "", "invalid_value"},
	} {
		args := map[string]any{"mode": "video", "items": []map[string]string{{"nodeId": "v1"}}, "selectionId": test.credential}
		if test.legacy != "" {
			args["logicalModelId"] = test.legacy
		}
		raw, _ := json.Marshal(args)
		_, err := s.decodeCloudAgentBatchSelection(test.user, string(raw))
		var field *cloudAgentFieldArgumentError
		if !errors.As(err, &field) || field.Field != "selectionId" || field.Issue != test.issue {
			t.Errorf("expected selectionId/%s, got %v", test.issue, err)
		}
	}
}

func TestCloudAgentBatchControlWithoutApprovalIsNotRepairable(t *testing.T) {
	s, _ := agentMediaBatchFixture(t)
	run, state := agentMediaBatchRun(t, s, agentMediaBatchArgs("v1"), 4)
	call := state.Calls[0]
	call.Function.Name, call.Function.Arguments = "media_batch_control", `{"action":"retry","items":[{"nodeId":"v1"}]}`
	if err := s.advanceCloudAgentMediaBatchControl(run, &state, call); err != nil {
		t.Fatal(err)
	}
	if len(state.ToolRepairs) != 0 {
		t.Fatalf("missing approval consumed parameter repair: %+v", state.ToolRepairs)
	}
	message := cloudAgentToolMessageForTest(t, state, call.ID)
	if !strings.Contains(message, "start_new_generation") {
		t.Fatalf("missing next action: %s", message)
	}
}

func TestCloudAgentArgumentFeedbackTargetsFailedObject(t *testing.T) {
	for _, test := range []struct {
		tool, args, field, want string
	}{
		{"canvas_apply_ops", `{"snapshotHash":"hash","ops":[{"type":"add_node","id":"n","nodeType":"text","workflowTitle":"wrong"}]}`, "ops[0].workflowTitle", "nodeType"},
		{"storyboard", `{"action":"write_rows","nodeId":"s","newRows":[{"shots":[]}]}`, "newRows[0].shots", "durationSeconds"},
	} {
		state := autoCorrectionFeedbackRuntime()
		call := cloudAgentCall{}
		call.Function.Name, call.Function.Arguments = test.tool, test.args
		detail := map[string]any{"field": test.field}
		cloudAgentArgumentFeedback(state, call, detail)
		hints, _ := detail["argumentHints"].(map[string]any)
		fields, _ := hints["allowedFields"].([]string)
		if !slices.Contains(fields, test.want) || slices.Contains(fields, "snapshotHash") {
			t.Errorf("%s: incorrect allowed fields %v", test.field, fields)
		}
	}
}

func TestCloudAgentCanvasOpCorrectionGuidance(t *testing.T) {
	for _, test := range []struct{ operation, field, hint, example string }{
		{`{"type":"add_node","id":"n","nodeType":"text","workflowTitle":"wrong"}`, "ops[0].workflowTitle", "update_node.patch", "add_node"},
		{`{"type":"update_node","id":"n","match":"a","replacement":"b","patch":{}}`, "ops[0].patch", "replace_text", "replace_text"},
		{`{"type":"update_node","id":"n","content":"private text"}`, "ops[0].patch", "patch", `"patch":{"content":"<content>"}`},
	} {
		state := autoCorrectionFeedbackRuntime()
		call := cloudAgentCall{}
		call.Function.Name, call.Function.Arguments = "canvas_apply_ops", `{"snapshotHash":"hash","ops":[`+test.operation+`]}`
		detail := map[string]any{"field": test.field}
		cloudAgentArgumentFeedback(state, call, detail)
		example, _ := json.Marshal(detail["exampleArguments"])
		decodedExample := strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(string(example))
		if !strings.Contains(stringValue(detail["guidance"]), test.hint) || !strings.Contains(decodedExample, test.example) {
			t.Errorf("%s: no directed guidance/example: %v, %s", test.field, detail["guidance"], example)
		}
	}
}

func autoCorrectionFeedbackRuntime() *cloudAgentRuntime {
	req := agentTestRequest()
	req.PermissionMode = "auto"
	state := &cloudAgentRuntime{Request: req}
	state.Canonical.Tools = cloudAgentTools(req)
	return state
}
