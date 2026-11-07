package app

import (
	"context"
	"encoding/json"
	"testing"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
)

func TestAgentSessionPiFiltersWorkToolAuthorization(t *testing.T) {
	f := newGenericPiFixture(t, "tool-filter", "drama")
	work, err := f.svc.repo.AgentWorkForOwner("user-a", f.assignment.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]any
	if err := json.Unmarshal([]byte(work.AuthorizationJSON), &auth); err != nil {
		t.Fatal(err)
	}
	auth["allowedTools"] = []string{"drama_summary"}
	raw, _ := json.Marshal(auth)
	if err := f.db.Model(&model.AgentWorkItem{}).Where("id = ?", work.ID).Update("authorization_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	enqueueGenericPi(t, f, "filtered", "读取")
	snapshot, err := f.svc.ClaimPiAgentForWorker("audit-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim: %v", err)
	}
	for _, tool := range snapshot.Tools {
		if tool.Name != "drama_summary" {
			t.Fatalf("unauthorized tool disclosed: %s", tool.Name)
		}
	}
}

func TestAgentSessionPiDoesNotStealLiveWriter(t *testing.T) {
	f := newGenericPiFixture(t, "ordered", "drama")
	first := enqueueGenericPi(t, f, "first", "第一条")
	snapshot, err := f.svc.ClaimPiAgentForWorker("ordered-worker", "user-a")
	if err != nil || snapshot == nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = f.svc.EnqueueAgentSessionPiRun("user-a", AgentSessionPiRunRequest{SessionID: f.session.ID, TriggerMessageID: "second", Prompt: "第二条"})
	if !as.IsCode(err, as.CodeSessionConflict) {
		t.Fatalf("second input stole writer: %v", err)
	}
	if _, err := f.svc.PiAgentSnapshot("user-a", first.RunID, "ordered-worker@"+itoa64(snapshot.PiSessionLeaseEpoch)); err != nil {
		t.Fatalf("first writer lost authority: %v", err)
	}
}

func TestAgentDramaShotReceiptAtomicRecovery(t *testing.T) {
	f := newGenericPiFixture(t, "atomic-shot", "drama")
	for _, row := range []any{
		&model.DramaEpisode{ID: "audit-ep", ProjectID: "drama-1", Number: 1},
		&model.DramaSegment{ID: "audit-seg", ProjectID: "drama-1", EpisodeID: "audit-ep", SegmentIndex: 0},
		&model.DramaShot{ID: "audit-shot", ProjectID: "drama-1", EpisodeID: "audit-ep", Dialogue: "before"},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	call := as.ToolInvocation{OwnerID: "user-a", SessionID: f.session.ID,
		Scope:      as.Scope{Application: "drama", SpaceType: "drama_project", SpaceID: "drama-1"},
		WorkItemID: f.assignment.WorkItemID, BudgetGrantID: f.assignment.BudgetGrantID,
		AssignmentID: f.assignment.ID, AssignmentEpoch: f.assignment.Epoch,
		Name: "drama_update_shot", CallID: "audit-shot-call", OperationID: "audit-shot-op",
		Arguments: json.RawMessage(`{"shotId":"audit-shot","dialogue":"agent edit"}`)}
	if err := f.db.Exec(`CREATE TRIGGER audit_receipt_fault BEFORE UPDATE ON agent_operations WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'receipt fault'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err == nil {
		t.Fatal("receipt fault was swallowed")
	}
	shot, err := f.svc.repo.DramaShot("drama-1", "audit-shot")
	if err != nil || shot.Dialogue != "before" {
		t.Fatalf("mutation escaped receipt rollback: %+v %v", shot, err)
	}
	if err := f.db.Exec(`DROP TRIGGER audit_receipt_fault`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	user := "later user edit"
	if _, err := f.svc.UpdateDramaShot("user-a", "drama-1", "audit-shot", UpdateDramaShotRequest{Dialogue: &user}); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := f.svc.executeBusinessTool(context.Background(), call); err != nil {
			t.Fatal(err)
		}
	}
	shot, err = f.svc.repo.DramaShot("drama-1", "audit-shot")
	if err != nil || shot.Dialogue != user {
		t.Fatalf("committed replay overwrote user: %+v %v", shot, err)
	}
}
