package app

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentTaskDiagnosticProjectsDurableExecutionFacts(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	now := time.Now().UTC().Truncate(time.Second)
	diagnostic := model.TaskExecutionDiagnostic{
		Code: "invalid_duration", Phase: "preparation", FieldPath: "generationSpec.options.durationSeconds",
		TaskSubmitted: false, SubmissionOutcome: "not_submitted", RetryClass: "correct_input",
		SafeMessage: "视频时长不受支持", DiagnosticID: "diag-1",
	}
	rawDiagnostic, _ := json.Marshal(diagnostic)
	task := &model.Task{
		ID: "task-facts", UserID: "user", ProjectID: "canvas", Type: "canvas_video", Status: model.TaskStatusFailed,
		Operation: "cloud_agent_generate_media", AgentRunID: "run-1", GenerationID: "generation-1", ApprovalID: "approval-1",
		BillingOrderID: "order-facts", AuthorizedChargeMicrocredits: 700, CancellationSource: model.TaskCancellationUserRequest,
		CancellationRequestedAt: &now, InputJSON: `{"metadata":{"source":"cloud_agent","nodeId":"video-node"}}`,
		ExecutionDiagnosticJSON: string(rawDiagnostic), CreatedAt: now,
	}
	order := &model.BillingOrder{
		ID: "order-facts", UserID: "user", TaskID: task.ID, Status: model.BillingStatusRefunded,
		ChargeLimitSet: true, ChargeLimitMicrocredits: 700, ReservedAmountMicrocredits: 700, RefundedAmountMicrocredits: 700,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}

	facts := cloudAgentTaskDiagnostic(s.repo, task)
	for key, want := range map[string]any{
		"taskId": task.ID, "taskStatus": string(model.TaskStatusFailed),
		"nodeId": "video-node", "taskSubmitted": false, "submissionOutcome": "not_submitted",
		"phase": "preparation", "diagnosticCode": "invalid_duration",
		"fieldPath": "generationSpec.options.durationSeconds", "retryClass": "correct_input", "error": "视频时长不受支持",
		"cancellationSource": model.TaskCancellationUserRequest,
	} {
		if got := facts[key]; got != want {
			t.Fatalf("%s = %#v, want %#v; facts=%#v", key, got, want, facts)
		}
	}
	billing, ok := facts["billing"].(map[string]any)
	if !ok || billing["status"] != string(model.BillingStatusRefunded) || billing["stateAvailable"] != true || billing["chargeState"] != "refunded" {
		t.Fatalf("billing facts = %#v", facts["billing"])
	}
	// 模型视图不带内部标识：这些值不会被任何工具参数回传。
	for _, key := range []string{"agentRunId", "generationId", "approvalId", "diagnosticId", "submissionReceipt", "cancellationRequestedAt"} {
		if _, leaked := facts[key]; leaked {
			t.Fatalf("model-facing task facts leaked %s: %#v", key, facts)
		}
	}
	for _, key := range []string{"orderId", "authorizedChargeMicrocredits", "chargeLimitSet", "chargeLimitMicrocredits", "reservedAmountMicrocredits", "refundedAmountMicrocredits"} {
		if _, leaked := billing[key]; leaked {
			t.Fatalf("model-facing billing leaked %s: %#v", key, billing)
		}
	}
}

func TestRecordTaskDiagnosticDoesNotClaimUnsubmittedRequest(t *testing.T) {
	task := &model.Task{ID: "task", Error: "参数无效"}
	recordTaskDiagnostic(task, "preparation", "preparation_failed", "not_submitted", "correct_input", nil)
	if task.Diagnostic == nil || task.Diagnostic.TaskSubmitted {
		t.Fatalf("diagnostic = %#v", task.Diagnostic)
	}
	recordTaskDiagnostic(task, "execution", "provider_execution_failed", "accepted", "new_generation_authorization", nil)
	if task.Diagnostic == nil || !task.Diagnostic.TaskSubmitted {
		t.Fatalf("diagnostic = %#v", task.Diagnostic)
	}
}

func TestCloudAgentTaskDiagnosticRejectsMismatchedBillingOrderFacts(t *testing.T) {
	_, db, _, _ := creationTestService(t)
	order := &model.BillingOrder{ID: "other-order", UserID: "other", TaskID: "other-task", Status: model.BillingStatusSettled}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "task", UserID: "user", Status: model.TaskStatusQueued, BillingOrderID: order.ID, AuthorizedChargeMicrocredits: 9}
	facts := cloudAgentTaskDiagnostic(repository.New(db), task)
	billing := facts["billing"].(map[string]any)
	if billing["stateAvailable"] != false || billing["status"] != nil {
		t.Fatalf("cross-owner billing state leaked: %#v", billing)
	}
}

func TestCloudAgentTaskDiagnosticSeparatesReservationFromActualCharge(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	task := &model.Task{
		ID: "task-settled", UserID: "user", ProjectID: "canvas", Status: model.TaskStatusSucceeded,
		BillingOrderID: "order-settled", InputJSON: `{}`,
	}
	order := &model.BillingOrder{
		ID: "order-settled", UserID: "user", TaskID: task.ID, Status: model.BillingStatusSettled,
		ReservedAmountMicrocredits: 100_000, ActualAmountMicrocredits: 100_000,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}

	facts := cloudAgentTaskDiagnostic(s.repo, task)
	billing, ok := facts["billing"].(map[string]any)
	if !ok {
		t.Fatalf("billing facts = %#v", facts["billing"])
	}
	if billing["reservedCredits"] != 0.1 || billing["actualCredits"] != 0.1 || billing["chargeState"] != "settled" {
		t.Fatalf("credit facts = %#v", billing)
	}
}
