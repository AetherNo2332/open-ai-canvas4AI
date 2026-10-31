package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// submission_uncertain 表示上游可能已接单并计费：它和 queued/running 一样仍占用这一轮，
// 不能被当成"已结束"去判卡死、接新轮或回收 worker。
func TestTaskStatusActive(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusSubmissionUncertain} {
		if !model.TaskStatusActive(status) {
			t.Fatalf("%s must be active", status)
		}
	}
	for _, status := range []model.TaskStatus{model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled} {
		if model.TaskStatusActive(status) {
			t.Fatalf("%s must not be active", status)
		}
	}
	if cloudAgentTaskTerminal(model.TaskStatusSubmissionUncertain) {
		t.Fatal("an uncertain submission is not a terminal task")
	}
}

func TestCloudAgentHasLiveWorkCountsUncertain(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	task := model.Task{ID: "uncertain-media", UserID: "user", Type: "canvas_video", Status: model.TaskStatusSubmissionUncertain}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	run := &model.CloudAgentExecution{ID: "run", UserID: "user"}
	if !s.cloudAgentHasLiveWork(run, &cloudAgentRuntime{MediaTaskID: task.ID}) {
		t.Fatal("a run waiting on an uncertain submission still has live work")
	}
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	if s.cloudAgentHasLiveWork(run, &cloudAgentRuntime{MediaTaskID: task.ID}) {
		t.Fatal("a failed task is not live work")
	}
}

func TestParentNotSupersededWhileUncertain(t *testing.T) {
	for _, test := range []struct {
		status     model.TaskStatus
		superseded bool
	}{
		{model.TaskStatusSubmissionUncertain, false},
		{model.TaskStatusFailed, true},
	} {
		t.Run(string(test.status), func(t *testing.T) {
			s, db, _, _ := creationTestService(t)
			if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", Title: "test", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
				t.Fatal(err)
			}
			root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
			if err != nil {
				t.Fatal(err)
			}
			_, state := agentStartPiModelStep(t, s, root.ID)
			if state.ActiveTaskID == "" {
				t.Fatal("fixture has no active task")
			}
			if err := db.Model(&model.Task{}).Where("id = ?", state.ActiveTaskID).Update("status", test.status).Error; err != nil {
				t.Fatal(err)
			}
			// 合同变更：让父轮的执行合同快照失效，只剩"有没有活动任务"决定能否接新轮。
			execution, err := s.repo.CloudAgent("user", root.ID)
			if err != nil {
				t.Fatal(err)
			}
			current := cloudAgentCapabilitySetHash()
			stale := strings.Replace(execution.StateJSON, current, strings.Repeat("0", len(current)), 1)
			if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("state_json", stale).Error; err != nil {
				t.Fatal(err)
			}
			if got := s.cloudAgentParentCanBeSuperseded("user", root.ID); got != test.superseded {
				t.Fatalf("superseded=%v, want %v", got, test.superseded)
			}
		})
	}
}

func TestCloudAgentMediaReceiptMarksUncertainNotRetryable(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	run, _ := queuePiMediaToolForTest(t, s, db, args, "request_approval")
	if err := advancePiAgentForTest(t, s, run.ID); err != nil {
		t.Fatal(err)
	}
	approveAgentMediaDraft(t, s, run.ID)
	run, state := agentInterjectionState(t, s, run.ID)
	taskID := state.MediaTaskID
	if taskID == "" {
		t.Fatal("media task missing")
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusSubmissionUncertain, "result_json": `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := advancePiAgentForTest(t, s, run.ID); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	// 异步生成：接单状态待核对以 generation_settled 与系统通知送达，通知要求先核对原任务、不得重新生成。
	var settled map[string]any
	for index := len(state.Events) - 1; index >= 0; index-- {
		if state.Events[index].Type == "generation_settled" && state.Events[index].Payload["taskId"] == taskID {
			settled = state.Events[index].Payload
			break
		}
	}
	if settled == nil || settled["status"] != string(model.TaskStatusSubmissionUncertain) {
		t.Fatalf("uncertain submission must be settled with its status: %+v", settled)
	}
	if len(state.PendingInterjections) != 1 || !strings.Contains(state.PendingInterjections[0].Text, "上游接单状态待核对") || !strings.Contains(state.PendingInterjections[0].Text, "不要重新生成") {
		t.Fatalf("notice must ask to verify the original submission instead of regenerating: %+v", state.PendingInterjections)
	}
}
