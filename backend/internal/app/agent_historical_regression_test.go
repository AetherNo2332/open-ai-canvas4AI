package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
)

func review4Assignment(t *testing.T, f *businessFixture, key string, scope as.Scope, definition string) (WorkView, AssignmentView) {
	t.Helper()
	root, err := f.svc.StartWork("user-a", StartWorkRequest{
		SourceMessageID: key, Goal: key, BudgetMicrocredits: 1_000_000,
		IdempotencyKey: key, AuthorizedScopes: []as.Scope{scope},
	})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{
		WorkItemID: root.ID, SourceMessageID: key, ActionKey: key,
		DefinitionID: definition, Scope: scope, Handoff: key, BudgetMicrocredits: 800_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, assignment
}

func review4Call(assignment AssignmentView, scope as.Scope, name, arguments string) as.ToolInvocation {
	return as.ToolInvocation{
		OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: scope,
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID,
		AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		CallID: name, Name: name, Arguments: json.RawMessage(arguments),
		OperationID: "review4:" + assignment.ExecutorSessionID + ":" + name,
	}
}

func TestAgentAuditSupervisorInputBoundToRoot(t *testing.T) {
	f := newBusinessFixture(t)
	dramaScope := as.Scope{Application: "drama", SpaceType: "drama_project", SpaceID: "drama-1"}
	rootA, _ := review4Assignment(t, f, "root-a", dramaScope, "drama.agent")
	ecommerceScope := as.Scope{Application: "ecommerce", SpaceType: "workbench_batch", SpaceID: "batch_review4"}
	_, childB := review4Assignment(t, f, "root-b", ecommerceScope, "ecommerce.agent")
	session, err := f.svc.agentSessionForOwner("user-a", rootA.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"workItemId": childB.WorkItemID, "text": "write into another root"})
	_, err = f.svc.executeSupervisorTool(context.Background(), session, as.ToolInvocation{
		OwnerID: "user-a", SessionID: session.ID, WorkItemID: rootA.ID,
		Name: supervisorToolAppendInput, CallID: "cross-root", OperationID: "review4:cross-root", Arguments: args,
	})
	if err == nil {
		t.Fatal("root A supervisor appended input to root B outside A's authorized scope")
	}
}

func TestAgentAuditMultiTaskWorkWaitsForEveryTask(t *testing.T) {
	f := newBusinessFixture(t)
	scope := as.Scope{Application: "ecommerce", SpaceType: "workbench_batch", SpaceID: "batch_multi4"}
	_, assignment := review4Assignment(t, f, "multi4", scope, "ecommerce.agent")
	for index := 0; index < 2; index++ {
		task, err := f.svc.createWorkbenchTaskUnderBudget("user-a", WorkbenchTaskInput{
			Kind: WorkbenchKindImage, Prompt: "cup", Model: f.modelKey, ChannelID: f.channelID,
			Parameters: map[string]any{"size": "1024x1024"},
		}, assignment.BudgetGrantID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.AgentOperation{
			ID: newID(), OwnerID: "user-a", SessionID: assignment.ExecutorSessionID,
			OperationKey: "multi4:" + task.ID, WorkItemID: assignment.WorkItemID,
			AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
			CallID: task.ID, Name: "ecommerce_generate_image", ArgumentsHash: "args",
			Status: agentOperationSucceeded, OperationRef: "task:" + task.ID,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := f.svc.repo.ClaimNextTask("review4-one-task", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := f.svc.taskWorker().processClaimedTask(claimed, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.reconcileAgentOperations("user-a"); err != nil {
		t.Fatal(err)
	}
	work, err := f.svc.GetWork("user-a", assignment.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if work.Status == ao.WorkCompleted {
		t.Fatal("work completed while its second generated image was still queued")
	}
}

func TestAgentAuditReportCannotCompletePendingGeneration(t *testing.T) {
	f := newBusinessFixture(t)
	scope := as.Scope{Application: "ecommerce", SpaceType: "workbench_batch", SpaceID: "batch_claim4"}
	_, assignment := review4Assignment(t, f, "claim4", scope, "ecommerce.agent")
	_, err := f.svc.createWorkbenchTaskUnderBudget("user-a", WorkbenchTaskInput{
		Kind: WorkbenchKindImage, Prompt: "cup", Model: f.modelKey, ChannelID: f.channelID,
		Parameters: map[string]any{"size": "1024x1024"},
	}, assignment.BudgetGrantID)
	if err != nil {
		t.Fatal(err)
	}
	call := review4Call(assignment, scope, "report_work", `{"status":"completed","summary":"all images ready"}`)
	_, err = f.svc.executeBusinessTool(context.Background(), call)
	if err == nil {
		t.Fatal("model-reported completed was accepted while a billed generation was still queued")
	}
}

func TestAgentAuditDramaRetryInheritsWorkBudget(t *testing.T) {
	f := newBusinessFixture(t)
	settings := DramaSettings{Models: DramaModels{
		Text:  DramaModelChoice{ChannelID: f.text.channel, ModelKey: f.text.modelKey},
		Image: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey},
		Video: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey},
	}}
	if err := f.db.Model(&model.DramaProduction{}).Where("project_id = ?", "drama-1").Update("settings_json", encodeDramaSettings(settings)).Error; err != nil {
		t.Fatal(err)
	}
	scope := as.Scope{Application: "drama", SpaceType: "drama_project", SpaceID: "drama-1"}
	_, assignment := review4Assignment(t, f, "drama-retry4", scope, "drama.agent")
	if err := f.db.Create(&model.DramaCast{ID: "review4-cast", ProjectID: "drama-1", Name: "actor", ImageStatus: model.DramaMediaFailed}).Error; err != nil {
		t.Fatal(err)
	}
	call := review4Call(assignment, scope, "drama_retry_failed", `{}`)
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	production, err := f.svc.repo.DramaProduction("drama-1")
	if err != nil {
		t.Fatal(err)
	}
	run := &dramaRun{s: f.svc, project: &model.Project{ID: "drama-1", UserID: "user-a"}, production: production,
		settings: settings, submitting: &model.DramaJob{ID: "review4-job", ProjectID: "drama-1", Kind: model.DramaJobCastImage, CastID: "review4-cast", Status: model.DramaJobQueued}}
	task, err := run.createTaskWith(settings.Models.Image, "image", "text_to_image", "actor", map[string]any{"config": map[string]any{"size": "1024x1024"}})
	if err != nil {
		t.Fatal(err)
	}
	var submission model.PlatformSubmission
	if err := f.db.First(&submission, "task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if submission.BudgetGrantID != assignment.BudgetGrantID {
		t.Fatalf("Agent retry generated a task outside work budget: grant=%q want=%q", submission.BudgetGrantID, assignment.BudgetGrantID)
	}
}
