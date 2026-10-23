package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStoryboardMutationLimitFacts(t *testing.T) {
	for _, limits := range []cloudAgentCallLimits{{}, cloudAgentCallLimitsForPolicy(defaultRuntimePolicy().Task)} {
		state := &cloudAgentRuntime{CallLimits: limits}
		message := state.callLimitFacts()
		var facts struct {
			CallLimits map[string]int `json:"callLimits"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(stringField(message, "content"), cloudAgentRuntimeContextMarker)), &facts); err != nil {
			t.Fatal(err)
		}
		want := map[string]int{"toolCallsPerStep": 8, "readCallsPerRun": 32, "stepsPerRun": 0, "mutationCallsPerStep": 1}
		if !reflect.DeepEqual(facts.CallLimits, want) {
			t.Errorf("model must see the independent mutation limit alongside existing defaults: got %+v, want %+v", facts.CallLimits, want)
		}
	}
}

func TestStoryboardWriteContinuationFeedback(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		for _, changed := range []bool{true, false} {
			changedRows := []any{}
			if changed {
				changedRows = append(changedRows, map[string]any{"rowId": "r1", "changedFields": []any{"dialogue"}})
			}
			original := map[string]any{
				"applied": true, "changed": changed, "affectedRows": 1,
				"changedRows": changedRows, "snapshotHash": "new-snapshot",
				"preview": cloudAgentApprovalPreview{},
			}
			receipt := feedbackResult(t, &cloudAgentRuntime{}, piAgentTestCall("write", "canvas_edit_storyboard", `{}`), original, nil)
			if receipt["applied"] != true || receipt["changed"] != changed || receipt["affectedRows"] != float64(1) || !reflect.DeepEqual(receipt["changedRows"], changedRows) || receipt["snapshotHash"] != "new-snapshot" {
				t.Fatalf("continuation lost mutation facts: %+v", receipt)
			}
			outcome := stringValue(receipt["outcome"])
			if !strings.Contains(outcome, "下一模型步骤") || !strings.Contains(outcome, "复用") || strings.Contains(outcome, "同一批") {
				t.Fatalf("successful write must allow snapshot reuse in the next model step: %+v", receipt)
			}
			if original["outcome"] != nil || original["preview"] == nil {
				t.Fatal("model projection mutated the original receipt")
			}
		}
	})
	t.Run("skipped", func(t *testing.T) {
		req := tierAutoRequest()
		state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
		calls := []cloudAgentCall{
			cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "first", map[string]any{"snapshotHash": "old", "nodeId": "board", "action": "update", "rowId": "r1", "patch": map[string]any{"dialogue": "one"}}),
			cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "second", map[string]any{"snapshotHash": "old", "nodeId": "board", "action": "update", "rowId": "r2", "patch": map[string]any{"dialogue": "two"}}),
		}
		admissions := cloudAgentPreflightBatch(state, calls)
		if !admissions[0].Allowed || admissions[1].Allowed || admissions[1].Issue != cloudAgentAdmissionSingleWrite {
			t.Fatalf("one-mutation guard changed: %+v", admissions)
		}
		receipt := feedbackResult(t, state, calls[1], nil, cloudAgentAdmissionError(admissions[1]))
		if receipt["reason"] != "call_skipped" || receipt["requiredAction"] != "resubmit_next_step" || receipt["admission"] != "single_write_per_step" {
			t.Fatalf("skip must remain an unexecuted call: %+v", receipt)
		}
		message := stringValue(receipt["error"])
		if !strings.Contains(message, "下一模型步骤") || !strings.Contains(message, "复用") || strings.Contains(message, "并先重新读取") {
			t.Fatalf("skip must point to next-step snapshot reuse without unconditional reread: %+v", receipt)
		}
		if len(state.ToolRepairs) != 0 {
			t.Fatalf("skip consumed argument repair budget: %+v", state.ToolRepairs)
		}
	})
}

func TestStoryboardReturnedSnapshotContinuesNextModelStep(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.IdempotencyKey = "storyboard-snapshot-continuation"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	first := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "continue-first", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "update", "rowId": stringValue(rows[0]["id"]), "patch": map[string]any{"dialogue": "first step"},
	})
	agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{first})
	if err := agentPiExecuteCallForTest(t, s, root.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	_, state := reloadAgentRun(t, s, root.ID)
	readCalls := state.ReadToolCalls
	var receipt map[string]any
	if err := json.Unmarshal([]byte(stringField(state.Canonical.Messages[len(state.Canonical.Messages)-1], "content")), &receipt); err != nil {
		t.Fatal(err)
	}
	if stringValue(receipt["snapshotHash"]) == "" {
		t.Fatalf("first step did not return a usable snapshot: %+v", receipt)
	}
	second := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "continue-second", map[string]any{
		"snapshotHash": receipt["snapshotHash"], "nodeId": "storyboard-1", "action": "update", "rowId": stringValue(rows[1]["id"]), "patch": map[string]any{"dialogue": "second step"},
	})
	agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{second})
	if err := agentPiExecuteCallForTest(t, s, root.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	_, state = reloadAgentRun(t, s, root.ID)
	if state.ReadToolCalls != readCalls {
		t.Fatalf("continuing from the returned snapshot required another read: before %d, after %d", readCalls, state.ReadToolCalls)
	}
	stored, err := s.repo.CanvasProjectForUser("user", canvas.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.PayloadJSON, "first step") || !strings.Contains(stored.PayloadJSON, "second step") {
		t.Fatalf("next model step did not preserve both writes: %s", stored.PayloadJSON)
	}
}
