package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestStoryboardAppendIdentitySurvivesRepeatedProviderIDs(t *testing.T) {
	for _, action := range []string{"append", "append_rows"} {
		for _, removedIndex := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/remove-%d", action, removedIndex), func(t *testing.T) {
				s, _, canvas := cloudAgentStoryboardFixture(t)
				policy, _ := s.RuntimePolicy()
				doc, _ := creationDocument(canvas.PayloadJSON)
				create := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "repeat", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "identity-board", "title": "identity", "rows": []map[string]any{{"durationSeconds": 3.0, "plotDescription": "one"}, {"durationSeconds": 3.0, "plotDescription": "two"}, {"durationSeconds": 3.0, "plotDescription": "three"}}})
				if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, create, policy); err != nil {
					t.Fatal(err)
				}
				load := func() (map[string]any, []map[string]any) {
					t.Helper()
					stored, err := s.repo.CanvasProjectForUser("user", canvas.ID)
					if err != nil {
						t.Fatal(err)
					}
					d, _ := creationDocument(stored.PayloadJSON)
					_, _, rows, err := storyboardNodeFromDocument(d, "identity-board")
					if err != nil {
						t.Fatal(err)
					}
					return d, rows
				}
				doc, rows := load()
				retiredID := rows[removedIndex]["id"]
				remove := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "remove", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "identity-board", "action": "remove", "rowId": retiredID})
				if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, remove, policy); err != nil {
					t.Fatal(err)
				}
				doc, _ = load()
				args := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "identity-board", "action": action}
				row := map[string]any{"durationSeconds": 3.0, "plotDescription": "fresh"}
				if action == "append" {
					args["patch"] = row
				} else {
					args["rows"] = []map[string]any{row}
				}
				original := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "repeat", args)
				state := cloudAgentRuntime{Request: tierAutoRequest(), Step: 2, LastStepTaskID: "model-2"}
				state.Request.CanvasID = canvas.ID
				resolved, err := cloudAgentResolveWorkflowIDs("user", "run", &state, original)
				if err != nil {
					t.Fatal(err)
				}
				if resolved.Function.Arguments != original.Function.Arguments || resolved.ID != original.ID {
					t.Fatal("resolution changed original wire call")
				}
				preview, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, resolved)
				if err != nil {
					t.Fatal(err)
				}
				_, _, planned, _ := storyboardNodeFromDocument(preview.Document, "identity-board")
				if len(planned) != 3 {
					t.Fatalf("invalid planned append: %v", planned)
				}
				if planned[2]["id"] == retiredID {
					t.Fatal("new operation reused retired row identity")
				}
				checkpoint, _ := json.Marshal(state)
				var restored cloudAgentRuntime
				if err := json.Unmarshal(checkpoint, &restored); err != nil {
					t.Fatal(err)
				}
				replayed, err := cloudAgentResolveWorkflowIDs("user", "run", &restored, original)
				if err != nil {
					t.Fatal(err)
				}
				replayPlan, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, replayed)
				if err != nil {
					t.Fatal(err)
				}
				_, _, replayRows, _ := storyboardNodeFromDocument(replayPlan.Document, "identity-board")
				if replayRows[2]["id"] != planned[2]["id"] {
					t.Fatal("checkpoint replay changed row identity")
				}
				if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, replayed, policy); err != nil {
					t.Fatalf("repeated provider ID collided: %v", err)
				}
				doc, rows = load()
				if len(rows) != 3 {
					t.Fatalf("append did not persist: %v", rows)
				}
				oldFreshID := rows[2]["id"]
				remove = cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "remove-2", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "identity-board", "action": "remove", "rowId": oldFreshID})
				if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, remove, policy); err != nil {
					t.Fatal(err)
				}
				doc, _ = load()
				args["snapshotHash"] = cloudAgentCanvasHash(doc)
				state.Step, state.LastStepTaskID = 3, "model-3"
				nextCall := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "repeat", args)
				nextCall, err = cloudAgentResolveWorkflowIDs("user", "run", &state, nextCall)
				if err != nil {
					t.Fatal(err)
				}
				nextPlan, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, nextCall)
				if err != nil {
					t.Fatal(err)
				}
				_, _, nextRows, _ := storyboardNodeFromDocument(nextPlan.Document, "identity-board")
				if nextRows[2]["id"] == oldFreshID {
					t.Fatal("new model step reused previous operation identity")
				}
			})
		}
	}
}

func TestStoryboardAppendIdentityApprovalReplay(t *testing.T) {
	for _, action := range []string{"append", "append_rows"} {
		t.Run(action, func(t *testing.T) {
			s, _, canvas := cloudAgentStoryboardFixture(t)
			createCloudAgentStoryboardForTest(t, s, canvas)
			req := agentTestRequest()
			req.PermissionMode, req.IdempotencyKey = "request_approval", "append-identity-approval"
			root, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			run, initial := startPiAgentFirstStep(t, s, root.ID)
			doc, _ := creationDocument(canvas.PayloadJSON)
			args := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": action}
			row := map[string]any{"durationSeconds": 4.0, "plotDescription": "approved fresh row"}
			if action == "append" {
				args["patch"] = row
			} else {
				args["rows"] = []map[string]any{row}
			}
			call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "repeat", args)
			state := *initial
			state.ActiveTaskID = ""
			state.Calls = []cloudAgentCall{call}
			state.CallIndex = 0
			state.ActivatedToolCategories = cloudAgentAppendActivatedCategory(state.ActivatedToolCategories, "agent_tools_canvas_edit")
			state.AdvertisedToolNames = cloudAgentToolNames(cloudAgentVisibleToolsForCategories(state.Canonical.Tools, state.ActivatedToolCategories, nil, nil))
			resolved, err := cloudAgentResolveWorkflowIDs("user", run.ID, &state, call)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, resolved)
			if err != nil {
				t.Fatal(err)
			}
			_, _, planned, err := storyboardNodeFromDocument(preview.Document, "storyboard-1")
			if err != nil {
				t.Fatal(err)
			}
			expectedID := planned[len(planned)-1]["id"]
			if cloudAgentApprovalCallHash(resolved) != cloudAgentApprovalCallHash(call) {
				t.Fatal("internal identity changed approval contract")
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			run, state = reloadAgentRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, &state); err != nil {
				t.Fatal(err)
			}
			waiting, err := s.CloudAgentRun("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if waiting.Status != "waiting_approval" || waiting.Approval == nil {
				t.Fatalf("append missed approval: %+v", waiting)
			}
			run, state = reloadAgentRun(t, s, run.ID)
			if state.Calls[0].Function.Arguments != call.Function.Arguments || state.Calls[0].storyboardMutationID != "" {
				t.Fatal("checkpoint changed model call or serialized internal identity")
			}
			if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "approve append"); err != nil {
				t.Fatal(err)
			}
			run, state = reloadAgentRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, &state); err != nil {
				t.Fatal(err)
			}
			stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
			doc, _ = creationDocument(stored.PayloadJSON)
			_, _, rows, err := storyboardNodeFromDocument(doc, "storyboard-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[2]["id"] != expectedID {
				t.Fatalf("approved execution identity drift: %v", rows)
			}
		})
	}
}

func TestStoryboardAppendIdentityScopeAndInternalBoundary(t *testing.T) {
	state := cloudAgentRuntime{Request: tierAutoRequest(), Step: 1, LastStepTaskID: "task-1"}
	state.Request.CanvasID = "canvas"
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "repeat", map[string]any{"nodeId": "board", "snapshotHash": "h", "action": "append", "patch": map[string]any{"durationSeconds": 3.0, "plotDescription": "new"}})
	one, err := cloudAgentResolveWorkflowIDs("user", "run", &state, call)
	if err != nil {
		t.Fatal(err)
	}
	if one.storyboardMutationID == "" {
		t.Fatal("append identity was not resolved")
	}
	for _, scenario := range []string{"step", "task", "run"} {
		nextState := state
		runID := "run"
		switch scenario {
		case "step":
			nextState.Step++
		case "task":
			nextState.LastStepTaskID = "task-2"
		case "run":
			runID = "other-run"
		}
		next, err := cloudAgentResolveWorkflowIDs("user", runID, &nextState, call)
		if err != nil {
			t.Fatal(err)
		}
		if next.storyboardMutationID == one.storyboardMutationID {
			t.Fatalf("%s did not scope append identity", scenario)
		}
	}
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	args["snapshotHash"] = "newer-snapshot"
	changed := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "repeat", args)
	changed, err = cloudAgentResolveWorkflowIDs("user", "run", &state, changed)
	if err != nil {
		t.Fatal(err)
	}
	if changed.storyboardMutationID != one.storyboardMutationID {
		t.Fatal("snapshot refresh changed operation identity")
	}
	raw, _ := json.Marshal(one)
	if strings.Contains(string(raw), one.storyboardMutationID) {
		t.Fatal("internal identity exposed on wire")
	}
	for _, payload := range []string{`{"id":"repeat","storyboardMutationID":"forged","function":{"name":"canvas_edit_storyboard","arguments":"{}"}}`, `{"id":"repeat","mutationId":"forged","function":{"name":"canvas_edit_storyboard","arguments":"{}"}}`} {
		var forged cloudAgentCall
		if err := decodeCloudAgentJSONObject(payload, &forged); err == nil {
			t.Fatal("client injected internal identity")
		}
	}
}

func TestStoryboardAppendDefensiveCollisionAllocation(t *testing.T) {
	for _, action := range []string{"append", "append_rows"} {
		rows := []map[string]any{{"id": cloudAgentID("user", "storyboard:board:seed:2"), "durationSeconds": 3.0, "plotDescription": "existing"}}
		args := cloudAgentStoryboardEditArgs{NodeID: "board", Action: action}
		row := map[string]any{"durationSeconds": 3.0, "plotDescription": "new"}
		if action == "append" {
			args.Patch = row
		} else {
			args.Rows = []map[string]any{row}
		}
		next, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, args, "user", "seed")
		if err != nil {
			t.Fatal(err)
		}
		if next[0]["id"] == next[1]["id"] {
			t.Fatal("defensive allocation retained duplicate ID")
		}
		replay, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, args, "user", "seed")
		if err != nil {
			t.Fatal(err)
		}
		if replay[1]["id"] != next[1]["id"] {
			t.Fatal("collision fallback is not deterministic")
		}
	}
}

func TestStoryboardCreativeFieldPersistenceParity(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	policy, _ := s.RuntimePolicy()
	content := func(stage string) map[string]any {
		row := map[string]any{"durationSeconds": float64(len(stage))}
		for field, contract := range capability.StoryboardRowFields() {
			if contract.JSONSchema()["type"] == "string" {
				row[field] = stage + ":" + field
			}
		}
		return row
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	apply := func(call cloudAgentCall) []map[string]any {
		t.Helper()
		if err := validateCloudAgentToolArguments(workflowTool(t, call.Function.Name), call.Function.Arguments); err != nil {
			t.Fatal(err)
		}
		if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err != nil {
			t.Fatal(err)
		}
		stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		doc, _ = creationDocument(stored.PayloadJSON)
		_, _, rows, err := storyboardNodeFromDocument(doc, "parity-board")
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := apply(cloudAgentStoryboardCall(t, "canvas_create_storyboard", "parity-create", map[string]any{"nodeId": "parity-board", "snapshotHash": cloudAgentCanvasHash(doc), "title": "parity", "rows": []map[string]any{content("create")}}))
	check := func(stage string) {
		t.Helper()
		for field, want := range content(stage) {
			t.Run(stage+"/"+field, func(t *testing.T) {
				if rows[0][field] != want {
					t.Fatalf("field %s persisted %v, want %v", field, rows[0][field], want)
				}
			})
		}
	}
	check("create")
	rows = apply(cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "parity-update", map[string]any{"nodeId": "parity-board", "snapshotHash": cloudAgentCanvasHash(doc), "action": "update", "rowId": rows[0]["id"], "patch": content("update")}))
	check("update")
	rows = apply(cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "parity-batch", map[string]any{"nodeId": "parity-board", "snapshotHash": cloudAgentCanvasHash(doc), "action": "batch_update", "updates": []map[string]any{{"rowId": rows[0]["id"], "patch": content("batch")}}}))
	check("batch")
}
