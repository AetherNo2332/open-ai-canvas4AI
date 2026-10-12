package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestPiNoToolCheckpointDefersPublicationUntilCompletionGate(t *testing.T) {
	for _, test := range []struct {
		name, stopKind, status string
		pendingPlan            bool
		published, final       bool
	}{
		{name: "completed", stopKind: "stop", status: "completed", published: true, final: true},
		{name: "pending plan", stopKind: "stop", status: "continue", pendingPlan: true, published: true},
		{name: "truncated", stopKind: "length", status: "continue"},
		{name: "refusal", stopKind: "refusal", status: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			const taskID = "projection-model-step"
			result, err := json.Marshal(map[string]any{"text": "模型候选正文", "stopReasonKind": test.stopKind})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID,
				Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(result)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.ActiveTaskID, state.LastStepTaskID = taskID, taskID
				state.TaskIDs = append(state.TaskIDs, taskID)
				if test.pendingPlan {
					state.Plan = []cloudAgentPlanItem{{ID: "pending", Title: "尚未完成的镜头", Status: "doing"}}
				}
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			checkpoint := PiMessageCheckpoint{Sequence: 1, TaskID: taskID,
				Message: json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"模型候选正文"}]}`)}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			_, checkpointed := reloadPiRun(t, s, run.ID)
			if agentHasEvent(*checkpointed, "assistant_message") {
				t.Fatal("无工具候选正文在收尾门禁前被发布")
			}
			decision, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, taskID)
			if err != nil || decision.Status != test.status {
				t.Fatalf("收尾判定 = %+v，错误 %v，期望 %s", decision, err, test.status)
			}
			before, state := reloadPiRun(t, s, run.ID)
			messages := agentEventPayloads(*state, "assistant_message")
			wantCount := 0
			if test.published {
				wantCount = 1
			}
			if len(messages) != wantCount {
				t.Fatalf("正文事件数 = %d，期望 %d", len(messages), wantCount)
			}
			if test.published && (messages[0]["text"] != "模型候选正文" || messages[0]["final"] != test.final) {
				t.Fatalf("正文终态投影错误: %+v", messages[0])
			}
			if _, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, taskID); err != nil {
				t.Fatalf("收尾重投失败: %v", err)
			}
			after, replayed := reloadPiRun(t, s, run.ID)
			if after.Revision != before.Revision || len(agentEventPayloads(*replayed, "assistant_message")) != wantCount {
				t.Fatal("收尾重投重复推进版本或发布正文")
			}
		})
	}
}
