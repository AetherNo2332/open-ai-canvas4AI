package app

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func piUsageAnchorTestRun(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	const userID, runID, canvasID = "user", "pi-usage-run", "pi-usage-canvas"
	req := agentTestRequest()
	req.CanvasID = canvasID
	req.PermissionMode = "auto"
	profile := cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")}
	system, policy, err := compileCloudAgentPolicies(req, nil, "", profile)
	if err != nil {
		t.Fatal(err)
	}
	state := cloudAgentRuntime{
		Request: req, Policy: policy, Profile: profile, Decisions: map[string]string{},
		TaskIDs: []string{"pi-usage-root-task"},
	}
	state.Canonical = cloudAgentCanonicalFor(system, nil, req.Prompt, req, false)
	state.StepLimits, err = s.cloudAgentStepLimits()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{
		ID: "pi-usage-root-task", UserID: userID, ProjectID: canvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: `{"text":""}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	run := &model.CloudAgentExecution{
		ID: runID, UserID: userID, Status: "running", Engine: "pi", Revision: 1,
		CanvasID: canvasID, ConversationID: runID, StateJSON: string(encoded),
	}
	if err := db.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{
		"type": "session", "version": 3, "id": runID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": "canvas://" + canvasID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentPiSession{
		ID: runID, UserID: userID, ConversationID: runID, CanvasID: canvasID,
		FormatVersion: 3, HeaderJSON: string(header), Revision: 1, ActiveRunID: runID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{ID: canvasID, UserID: userID, PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPiAgent("worker-usage")
	if err != nil || claimed == nil {
		t.Fatalf("claim Pi usage test run: snapshot=%#v error=%v", claimed, err)
	}
	run, err = s.repo.CloudAgent(userID, runID)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, run
}

func TestPiAssistantCheckpointPersistsProviderUsageAnchor(t *testing.T) {
	for _, usageAvailable := range []bool{true, false} {
		name := "usage unavailable"
		if usageAvailable {
			name = "provider usage available"
		}
		t.Run(name, func(t *testing.T) {
			s, db, run := piUsageAnchorTestRun(t)
			const taskID = "pi-model-step"
			if err := db.Create(&model.Task{
				ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
				Status: model.TaskStatusSucceeded, ResultJSON: `{"mode":"text","text":"answer"}`,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ApiCallLog{
				ID: "pi-model-step-usage", UserID: "user", TaskID: taskID, Capability: "text",
				Model: "text-test", Status: model.ApiCallStatusSucceeded,
				InputTokens: 10000, OutputTokens: 120, UsageAvailable: usageAvailable,
			}).Error; err != nil {
				t.Fatal(err)
			}

			state, err := cloudAgentDecode(run)
			if err != nil {
				t.Fatal(err)
			}
			state.ActiveTaskID = taskID
			state.TaskIDs = append(state.TaskIDs, taskID)
			state.LastStepTaskID = taskID
			state.LastStepOperation = cloudAgentStepOperation
			state.LastStepEstimate = 40 // Provider counters are authoritative even when the heuristic disagrees.
			state.LastStepPressure = &cloudAgentContextPressure{EstimatedInputTokens: 40, ModelLimitConfigured: true,
				UsableInputTokens: 100000, InputBudgetTokens: 100000, CompactAtTokens: 85000, ContextWindowTokens: 128000}
			state.LastStepSourceBytes = 40000
			state.LastStepSignature = cloudAgentRequestSignature(&state, state.Canonical, "", "text-test")
			state.LastStepModel = "text-test"
			state.Step = 2
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("state_json", string(encoded)).Error; err != nil {
				t.Fatal(err)
			}

			message := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"answer"}]}`)
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{
				Sequence: 1, Message: message, TaskID: taskID,
			}); err != nil {
				t.Fatalf("assistant checkpoint failed: %v", err)
			}
			persistedRun, err := s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := cloudAgentDecode(persistedRun)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.ActiveTaskID != "" {
				t.Fatalf("model task was not acknowledged with the checkpoint: %q", persisted.ActiveTaskID)
			}
			if usageAvailable {
				if persisted.TokenAnchor == nil || !persisted.TokenAnchor.Accepted || persisted.TokenAnchor.TaskID != taskID {
					t.Fatalf("Pi checkpoint did not persist provider usage anchor: %+v", persisted.TokenAnchor)
				}
				pressure := cloudAgentContextPressurePayload(cloudAgentContextPressure{EstimatedInputTokens: 10000}, &persisted)
				if pressure["tokenSource"] != "provider" {
					t.Fatalf("next-step pressure should use provider anchor: %+v", pressure)
				}
				measuredEvents := 0
				for _, event := range persisted.Events {
					if event.Type == "context_pressure" && event.Payload["phase"] == "after_request" {
						measuredEvents++
						if event.Payload["requestId"] != taskID || event.Payload["projectedTokens"] != float64(10000) {
							t.Fatalf("completed step must publish its exact usage: %+v", event.Payload)
						}
						if event.Payload["pressureRatio"] != 0.1 || event.Payload["compactionPressureRatio"] != 0.1 || event.Payload["readingScope"] != "latest_provider_request" {
							t.Fatalf("completion must publish the same budget and measured pressure: %+v", event.Payload)
						}
					}
				}
				if measuredEvents != 1 {
					t.Fatalf("even a final assistant step must publish one measured pressure event, got %d", measuredEvents)
				}
			} else if persisted.TokenAnchor != nil {
				t.Fatalf("provider usage was unavailable but Pi checkpoint created an anchor: %+v", persisted.TokenAnchor)
			}
		})
	}
}
