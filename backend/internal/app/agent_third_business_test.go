package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAgentSDKThirdBusinessReadAndRecovery(t *testing.T) {
	s, db := agentRunFixture(t)
	scope := AgentBusinessScope{Type: "library", ID: "licensed-shelf"}
	readCount := 0
	registry, err := newAgentDefinitionRegistry([]AgentDefinition{{
		ID: "library.reader", Version: 1, BusinessType: "library", IsDefault: true, ReadOnly: true,
		BusinessTools: []string{}, ReadAdapters: []string{"library:document"},
		authorize: func(_ *Service, user string, request CloudAgentRequest) (*model.CanvasProject, error) {
			if user != "user" || agentBusinessScope(request) != scope {
				return nil, kernel.NotFound("书架不可访问")
			}
			return nil, nil
		},
	}}, agentReadAdapter{
		namespace: "library:document", business: "library", version: 1,
		read: func(_ *Service, _ string, _ *cloudAgentRuntime, id string, _ agentReadArguments, _ cloudAgentCall) (agentReadResult, error) {
			readCount++
			if id != "chapter-1" {
				return agentReadResult{}, kernel.NotFound("文档不存在")
			}
			return agentReadResult{fields: map[string]any{"content": "THIRD_BUSINESS_CHAPTER"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.agentDefinitions = registry
	req := agentTestRequest()
	req.Scope, req.CanvasID, req.ContextScope = &scope, "", nil
	req.Prompt = "读取 library:document/chapter-1"
	if _, err := s.CreateCloudAgentRun("foreign", req, ""); err == nil {
		t.Fatal("new business object authorizer did not run")
	}
	created, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, state := startPiAgentFirstStep(t, s, created.ID)
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("third-read", "read", `{"path":"library:document/chapter-1"}`)
	taskID := state.ActiveTaskID
	result, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(result),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.ActiveTaskID, state.LastStepTaskID = "", taskID
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
		t.Fatal(err)
	}
	read, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID)
	if err != nil || !strings.Contains(string(read.Result), "THIRD_BUSINESS_CHAPTER") {
		t.Fatalf("registered tool failed: %+v %v", read, err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).UpdateColumn("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", snapshot.PiSessionID).UpdateColumn("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ClaimPiAgent("worker-b")
	if err != nil || recovered == nil || recovered.RunID != run.ID || recovered.PiSessionID != snapshot.PiSessionID {
		t.Fatalf("worker did not recover the registered session: %+v %v", recovered, err)
	}
	latest, state := reloadPiRun(t, s, run.ID)
	replayed, err := s.PiToolAdvance("user", run.ID, latest.LeaseOwner, taskID, call.ID)
	if err != nil || string(replayed.Result) != string(read.Result) || readCount != 1 {
		t.Fatalf("durable receipt replay changed: reads=%d, %+v %v", readCount, replayed, err)
	}
	next, _ := piFirstStepRequest(state)
	step, err := s.PiModelStep("user", run.ID, latest.LeaseOwner, next)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.TaskForUser("user", step.TaskID)
	if err != nil || !strings.Contains(task.InputJSON, "THIRD_BUSINESS_CHAPTER") || task.ProjectID != scope.ID {
		t.Fatalf("registered tool output was not sent to the model task: %v", err)
	}
}
