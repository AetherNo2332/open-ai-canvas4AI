package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestPiNativeMalformedReadRequiresExactBilledRawFailure(t *testing.T) {
	for _, mode := range []string{"malformed", "valid-spoof", "raw-mismatch", "claim-success", "marker-array", "annotation-array", "annotation-null-empty"} {
		t.Run(mode, func(t *testing.T) {
			s, db, run, frozen := nativeSkillLeasedFixture(t)
			raw := `{"path":"private-cookie"`
			if mode == "annotation-null-empty" {
				raw = ""
			}
			expectedRaw := raw
			if mode == "valid-spoof" {
				expectedRaw = `{"path":"C:/old/skills/` + frozen.NativeName + `/SKILL.md"}`
			}
			if mode == "raw-mismatch" {
				expectedRaw += " "
			}
			const taskID = "pi-native-malformed-result"
			call := piAgentTestCall("native-malformed", "read", expectedRaw)
			output, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil {
				t.Fatal(err)
			}
			state, err := cloudAgentDecode(run)
			if err != nil {
				t.Fatal(err)
			}
			state.LastStepTaskID, state.ActiveTaskID = taskID, taskID
			state.TaskIDs = append(state.TaskIDs, taskID)
			if err := db.Model(run).Update("state_json", marshalNativeTestState(t, state)).Error; err != nil {
				t.Fatal(err)
			}
			var arguments any = map[string]any{"__canvasInvalidToolArguments": true}
			var annotation any = map[string]any{"rawArguments": raw}
			if mode == "marker-array" {
				arguments = []any{}
			}
			if mode == "annotation-array" {
				annotation = []any{}
			}
			if mode == "annotation-null-empty" {
				annotation = map[string]any{"rawArguments": nil}
			}
			assistant, _ := json.Marshal(map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "toolCall", "id": call.ID, "name": "read", "arguments": arguments,
				"canvasInvalidArguments": annotation,
			}}})
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 1, TaskID: taskID, Message: assistant}); err != nil {
				t.Fatal(err)
			}
			result, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": call.ID, "toolName": "read", "isError": mode != "claim-success",
				"content": []any{map[string]any{"type": "text", "text": "invalid_tool_arguments"}}})
			checkpoint := PiMessageCheckpoint{Sequence: 2, Message: result}
			err = s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint)
			if mode != "malformed" {
				if err == nil {
					t.Fatal("unverified native raw rejection was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			before, saved := reloadPiRun(t, s, run.ID)
			eventsJSON, _ := json.Marshal(saved.Events)
			if strings.Contains(string(eventsJSON), "private-cookie") {
				t.Fatal("raw arguments leaked to an event")
			}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			after, replay := reloadPiRun(t, s, run.ID)
			if len(before.Transcript) != len(after.Transcript) || len(saved.Events) != len(replay.Events) {
				t.Fatal("native failure replay duplicated a result")
			}
		})
	}
}

func TestPiMalformedArgumentsHaveAuthoritativeRejectionReceipts(t *testing.T) {
	for _, raw := range []string{`{"rows":[{"dialogue":"bad "quote""}]}`, "", "[]", "null", "true", `"text"`} {
		t.Run(raw, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			call := piAgentTestCall("bad-call", "canvas_get_state", raw)
			output, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
			const taskID = "pi-malformed-model-result"
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.LastStepTaskID = taskID
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			batch := PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}
			if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
				t.Fatalf("billed malformed call must be admitted only for rejection: %v", err)
			}
			_, state := reloadPiRun(t, s, run.ID)
			admission, ok := cloudAgentAdmissionFor(state, 0)
			if !ok || admission.Allowed || admission.Issue != "schema_error" || admission.Field != "arguments" {
				t.Fatalf("invalid arguments escaped authoritative preflight: %+v", admission)
			}
			message, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": call.ID, "toolName": call.Function.Name, "isError": true,
				"content": []map[string]any{{"type": "text", "text": "invalid_tool_arguments"}}})
			checkpoint := PiMessageCheckpoint{Sequence: 1, Message: message}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			before, state := reloadPiRun(t, s, run.ID)
			if receipt := piToolReceipt(state, taskID, call.ID); receipt == nil || !receipt.IsError || state.CallIndex != 1 {
				t.Fatalf("rejection was not durably paired: index=%d receipt=%+v", state.CallIndex, receipt)
			}
			if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
				t.Fatalf("replay failed: %v", err)
			}
			afterBatch, _ := reloadPiRun(t, s, run.ID)
			if before.Revision != afterBatch.Revision {
				t.Fatal("batch replay mutated its state")
			}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			after, replay := reloadPiRun(t, s, run.ID)
			if len(before.Transcript) != len(after.Transcript) || replay.CallIndex != 1 || len(replay.Events) != len(state.Events) ||
				!reflect.DeepEqual(piToolReceipt(replay, taskID, call.ID), piToolReceipt(state, taskID, call.ID)) {
				t.Fatal("replay repeated a settled call")
			}
			tampered := PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{piAgentTestCall(call.ID, call.Function.Name, " "+raw)}}
			if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, tampered); err == nil {
				t.Fatal("malformed raw bytes were changed")
			}
			if validateCloudAgentCalls([]cloudAgentCall{call}) == nil {
				t.Fatal("legacy call validation was weakened")
			}
			legacyRun := *after
			legacyRun.Engine = "legacy"
			if validateCloudAgentRuntime(&legacyRun, replay) == nil {
				t.Fatal("legacy runtime accepted malformed arguments from Pi rejection state")
			}
			for _, corruption := range []string{"no-pi-task", "allowed", "wrong-call"} {
				copyState := *replay
				copyState.CallAdmissions = append([]cloudAgentCallAdmission(nil), replay.CallAdmissions...)
				switch corruption {
				case "no-pi-task":
					copyState.PiToolBatchTaskID = ""
				case "allowed":
					copyState.CallAdmissions[0].Allowed = true
				case "wrong-call":
					copyState.CallAdmissions[0].CallID = "wrong"
				}
				if validateCloudAgentRuntime(after, &copyState) == nil {
					t.Fatalf("invalid raw state accepted with %s", corruption)
				}
			}
		})
	}
}

func TestPiMixedMalformedBatchPreservesSuccessfulWriteAcrossRestart(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := db.Create(&model.User{ID: "user", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	write := parityCall("canvas_apply_ops", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "ops": []any{
		map[string]any{"type": "add_node", "id": "once", "nodeType": "text", "content": "write once"},
	}})
	write.ID = "written"
	invalid := piAgentTestCall("rejected", "canvas_apply_ops", `{"dialogue":"bad "quote""}`)
	calls := []cloudAgentCall{write, invalid}
	const taskID = "pi-mixed-malformed-model-result"
	output, _ := json.Marshal(map[string]any{"toolCalls": calls})
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Canonical.Tools = cloudAgentTools(state.Request)
		state.ActivatedToolCategories = []string{"agent_tools_canvas_edit"}
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	batch := PiToolBatchRequest{TaskID: taskID, Calls: calls}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatal(err)
	}
	_, admitted := reloadPiRun(t, s, run.ID)
	if !admitted.CallAdmissions[0].Allowed || admitted.CallAdmissions[1].Allowed {
		t.Fatalf("mixed batch admissions: %+v", admitted.CallAdmissions)
	}
	// An untrusted SDK error cannot skip a valid admitted write.
	spoof, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": write.ID, "toolName": write.Function.Name, "isError": true})
	piSettleLocallyRejectedTool(run, admitted, spoof)
	if admitted.CallIndex != 0 || piToolReceipt(admitted, taskID, write.ID) != nil {
		t.Fatal("worker error settled a valid business call")
	}
	first, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, write.ID)
	if err != nil || first.Pending || first.IsError {
		t.Fatalf("real canvas write failed: %+v, %v", first, err)
	}
	checkpoint := func(sequence int, call cloudAgentCall, isError bool) PiMessageCheckpoint {
		message, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": call.ID, "toolName": call.Function.Name, "isError": isError,
			"content": []any{map[string]any{"type": "text", "text": "durable tool result"}}})
		return PiMessageCheckpoint{Sequence: sequence, Message: message}
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint(1, write, false)); err != nil {
		t.Fatal(err)
	}
	before, err := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the service, then resume only the missing malformed call.
	restarted := &Service{repo: repository.New(db), dataDir: s.dataDir}
	if err := restarted.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatal(err)
	}
	if err := restarted.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint(2, invalid, true)); err != nil {
		t.Fatal(err)
	}
	_, settled := reloadPiRun(t, restarted, run.ID)
	if settled.CallIndex != 2 || piToolReceipt(settled, taskID, invalid.ID) == nil || !piToolReceipt(settled, taskID, invalid.ID).IsError {
		t.Fatalf("malformed call was not durably closed: index=%d", settled.CallIndex)
	}
	for _, call := range calls {
		if receipt, err := restarted.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID); err != nil || receipt.Pending {
			t.Fatalf("receipt replay failed: %+v, %v", receipt, err)
		}
	}
	after, err := restarted.repo.CanvasProjectForUser("user", run.CanvasID)
	if err != nil || after.Revision != before.Revision || after.PayloadJSON != before.PayloadJSON {
		t.Fatalf("restart repeated a successful write: before=%d after=%+v err=%v", before.Revision, after, err)
	}
	actual, err := creationDocument(after.PayloadJSON)
	if err != nil || len(actual["nodes"].([]any)) != 1 {
		t.Fatalf("real canvas did not contain exactly one written node: %+v, %v", actual, err)
	}
}

func TestPiMalformedArgumentEnvelopeRetainsBoundsAndIdentity(t *testing.T) {
	valid := piAgentTestCall("call", "canvas_get_state", strings.Repeat("x", 32000))
	if err := validatePiToolBatchEnvelope([]cloudAgentCall{valid}); err != nil {
		t.Fatal(err)
	}
	for _, calls := range [][]cloudAgentCall{
		{valid, valid},
		{piAgentTestCall("", "canvas_get_state", "{")},
		{piAgentTestCall("call", strings.Repeat("x", 81), "{")},
		{piAgentTestCall("call", "canvas_get_state", strings.Repeat("x", 32001))},
		{piAgentTestCall("call", "canvas_get_state", string([]byte{0xff}))},
	} {
		if validatePiToolBatchEnvelope(calls) == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	if piSameToolArguments("[]", " []") || piSameToolArguments("{", "{}") || !piSameToolArguments(`{"a":1}`, `{ "a":1 }`) {
		t.Fatal("raw argument identity contract changed")
	}
}

func TestPiMalformedArgumentsCannotExecuteWithoutParameterSchema(t *testing.T) {
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	for _, raw := range []string{"{", "", "[]", "null", "true", `"text"`} {
		admissions := cloudAgentPreflightBatch(state, []cloudAgentCall{piAgentTestCall("call", "canvas_get_state", raw)})
		if len(admissions) != 1 || admissions[0].Allowed || admissions[0].Issue != "schema_error" {
			t.Fatalf("nil parameter schema permitted invalid arguments: %+v", admissions)
		}
	}
	if admissions := cloudAgentPreflightBatch(state, []cloudAgentCall{piAgentTestCall("call", "canvas_get_state", "{}")}); !admissions[0].Allowed {
		t.Fatal("valid zero-argument object was rejected")
	}
}
