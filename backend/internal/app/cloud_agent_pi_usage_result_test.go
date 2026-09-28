package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestPiModelStepViewPreservesProviderUsageAndUnknown(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	tests := []struct {
		name       string
		resultJSON string
		wantUsage  map[string]any
	}{
		{
			name:       "reported usage",
			resultJSON: `{"mode":"text","text":"answer","usage":{"input":12,"output":5,"totalTokens":17}}`,
			wantUsage:  map[string]any{"input": float64(12), "output": float64(5), "totalTokens": float64(17)},
		},
		{
			name:       "usage absent",
			resultJSON: `{"mode":"text","text":"answer"}`,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			taskID := "usage-step-" + string(rune('a'+index))
			if err := db.Create(&model.Task{ID: taskID, UserID: run.UserID, ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: test.resultJSON}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.ActiveTaskID = taskID
				state.TaskIDs = append(state.TaskIDs, taskID)
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			latest, err := s.repo.CloudAgent(run.UserID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Revision = latest.Revision
			view, err := s.PiModelStepView(run.UserID, run.ID, run.LeaseOwner, taskID)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(view.Result, &result); err != nil {
				t.Fatal(err)
			}
			usage, hasUsage := result["usage"]
			if test.wantUsage == nil {
				if hasUsage {
					t.Fatalf("PiModelStepView fabricated usage: %#v", usage)
				}
				return
			}
			if !hasUsage {
				t.Fatal("PiModelStepView dropped provider usage")
			}
			if got, ok := usage.(map[string]any); !ok || !equalJSONNumbers(got, test.wantUsage) {
				t.Fatalf("PiModelStepView usage = %#v, want %#v", usage, test.wantUsage)
			}
		})
	}
}

func equalJSONNumbers(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
