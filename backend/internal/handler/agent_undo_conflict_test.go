package handler

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

// The conflict envelope: HTTP 409, business code 409 and reason
// agent_undo_conflict, with the canvas left as it was.
func TestAgentRunUndoHTTPConflictEnvelope(t *testing.T) {
	call, svc, db := workspaceHTTPFixture(t)
	capability, err := json.Marshal(service.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.ModelChannel{ID: "channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "受控测试"},
		&model.ChannelModel{ID: "cm", ChannelID: "channel", ModelKey: "text-test", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: string(capability), BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "tier", ChannelModelID: "cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
		&model.CreditAccount{UserID: "workspace-user", AvailableMicrocredits: 10000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := service.CloudAgentRequest{CanvasID: "own", Prompt: "整理画布", Model: "text-test", ChannelID: "channel", ChannelModelKey: "text-test", PermissionMode: "read_only", ContextScope: []string{"canvas"}, IdempotencyKey: "undo-conflict-key"}
	req.Budget.MaxCredits = 1
	run, err := svc.CreateCloudAgentRun("workspace-user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	before := `{"nodes":[{"id":"n1","type":"text","title":"节点","metadata":{"content":"原文"}}],"connections":[]}`
	after := `{"nodes":[{"id":"n1","type":"text","title":"节点","metadata":{"content":"Agent 改写"}}],"connections":[]}`
	live := `{"nodes":[{"id":"n1","type":"text","title":"节点","metadata":{"content":"用户之后又改"}}],"connections":[]}`
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "own").Update("payload_json", live).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentCanvasMutation{ID: "m1", RunID: run.ID, UserID: "workspace-user", CanvasID: "own", StepID: "call", Operation: "canvas_apply_ops", BeforeSnapshotHash: "b", AfterSnapshotHash: "a", BeforeJSON: before, AfterJSON: after, Status: "applied", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}

	w := call("POST", "/api/agent/runs/"+run.ID+"/undo", `{"reason":"撤销"}`, true)
	var body struct {
		Code    int            `json:"code"`
		Reason  string         `json:"reason"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || body.Code != 409 || body.Reason != "agent_undo_conflict" {
		t.Fatalf("conflict envelope: %d %s", w.Code, w.Body)
	}
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", "own").Error; err != nil || canvas.PayloadJSON != live {
		t.Fatalf("conflicting undo must not write the canvas: %v %s", err, canvas.PayloadJSON)
	}
}
