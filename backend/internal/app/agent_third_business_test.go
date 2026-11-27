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

// TestAgentSDKThirdBusinessReadAndRecovery 证明新业务只需注册一个具名工具
// <business>_<action>_<object>，就能拿到与画布、短剧一致的授权、回执和恢复语义。
func TestAgentSDKThirdBusinessReadAndRecovery(t *testing.T) {
	s, db := agentRunFixture(t)
	scope := AgentBusinessScope{Type: "library", ID: "licensed-shelf"}
	readCount := 0
	compiler := &cloudAgentToolCompiler{}
	compiler.add("library_read_document", "读取测试书架中的一份文档", map[string]any{
		"documentId": map[string]any{"type": "string"},
	}, "documentId")
	registration := agentToolRegistration{
		name: "library_read_document", businesses: []string{"library"}, version: 1, schema: compiler.tools[0],
		execute: func(service *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, _ bool) error {
			readCount++
			var args struct {
				DocumentID string `json:"documentId"`
			}
			if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
				return service.recordCloudAgentToolReceipt(run, state, call, nil, cloudAgentJSONArgumentError(err))
			}
			if args.DocumentID != "chapter-1" {
				return service.recordCloudAgentToolReceipt(run, state, call, nil, kernel.NotFound("文档不存在"))
			}
			return service.recordCloudAgentToolReceipt(run, state, call, map[string]any{
				"documentId": args.DocumentID, "content": "THIRD_BUSINESS_CHAPTER", "adopted": false,
			}, nil)
		},
	}
	registry, err := newAgentRegistry([]AgentDefinition{{
		ID: "library.reader", Version: 1, BusinessType: "library", IsDefault: true, ReadOnly: true,
		BusinessTools: []string{"library_read_document"},
		authorize: func(_ *Service, user string, request CloudAgentRequest) (*model.CanvasProject, error) {
			if user != "user" || agentBusinessScope(request) != scope {
				return nil, kernel.NotFound("书架不可访问")
			}
			return nil, nil
		},
	}}, []agentToolRegistration{registration})
	if err != nil {
		t.Fatal(err)
	}
	s.agentDefinitions = registry
	req := agentTestRequest()
	req.Scope, req.CanvasID, req.ContextScope = &scope, "", nil
	req.Prompt = "读取 chapter-1"
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
	if !containsToolName(piSnapshotToolNames(snapshot), "library_read_document") {
		t.Fatalf("registered business tool was not disclosed: %v", piSnapshotToolNames(snapshot))
	}
	call := piAgentTestCall("third-read", "library_read_document", `{"documentId":"chapter-1"}`)
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

func piSnapshotToolNames(snapshot *PiAgentSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	names := make([]string, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		names = append(names, tool.Name)
	}
	return names
}
