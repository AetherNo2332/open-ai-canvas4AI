package app

import (
	"context"
	"errors"
	"testing"

	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

func TestEcommerceGenerationEntryKeepsReplayOnOriginalProject(t *testing.T) {
	svc, db := mockEcommerceAgentService(t)
	req := mockEcommerceRequest()
	req.IdempotencyKey = "batch6-replay"
	view, err := svc.GenerateEcommerceAgent(context.Background(), "owner", req, func(ecommerceagent.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	other, err := svc.CreateEcommerceProject("owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(other).Update("draft_json", `{"marker":"untouched"}`).Error; err != nil {
		t.Fatal(err)
	}
	req.ProjectID = other.ID
	req.Query = "同键但不同请求"
	req.SelectedRecommendationID = "r1"
	replayed, err := svc.GenerateEcommerceAgent(context.Background(), "owner", req, func(ecommerceagent.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Run.ID != view.Run.ID || replayed.Run.ProjectID != view.Run.ProjectID {
		t.Fatalf("replay run=%+v original=%+v", replayed.Run, view.Run)
	}

	var projects, runs, orders int64
	db.Model(&model.EcommerceProject{}).Count(&projects)
	db.Model(&model.EcommerceAgentRun{}).Count(&runs)
	db.Model(&model.BillingOrder{}).Count(&orders)
	if projects != 2 || runs != 1 || orders != 1 {
		t.Fatalf("projects=%d runs=%d orders=%d", projects, runs, orders)
	}
	var untouched model.EcommerceProject
	if err := db.First(&untouched, "id = ?", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.DraftJSON != `{"marker":"untouched"}` {
		t.Fatalf("replay changed new project: %s", untouched.DraftJSON)
	}
	account, err := svc.repo.CreditAccount("owner")
	if err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 840_000 || account.ReservedMicrocredits != 0 {
		t.Fatalf("replay account=%+v", account)
	}
}

func TestEcommerceGenerationEntryRejectsForeignProjectWithoutBilling(t *testing.T) {
	svc, db := mockEcommerceAgentService(t)
	foreign := &model.EcommerceProject{ID: "foreign-batch6", UserID: "other", Name: "foreign", Status: "draft", DraftJSON: "{}", PlanJSON: "{}", Revision: 1}
	if err := db.Create(foreign).Error; err != nil {
		t.Fatal(err)
	}
	req := mockEcommerceRequest()
	req.IdempotencyKey = "batch6-foreign"
	req.ProjectID = foreign.ID
	if _, err := svc.GenerateEcommerceAgent(context.Background(), "owner", req, func(ecommerceagent.Event) error { return nil }); err == nil {
		t.Fatal("foreign project was accepted")
	}

	var analyses, runs, orders, canvases int64
	db.Model(&model.EcommerceAgentAnalysis{}).Count(&analyses)
	db.Model(&model.EcommerceAgentRun{}).Count(&runs)
	db.Model(&model.BillingOrder{}).Count(&orders)
	db.Model(&model.EcommerceAgentCanvasVersion{}).Count(&canvases)
	if analyses != 0 || runs != 0 || orders != 0 || canvases != 0 {
		t.Fatalf("analyses=%d runs=%d orders=%d canvases=%d", analyses, runs, orders, canvases)
	}
	account, err := svc.repo.CreditAccount("owner")
	if err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 1_000_000 || account.ReservedMicrocredits != 0 {
		t.Fatalf("foreign project account=%+v", account)
	}
}

func TestEcommerceGenerationEntryFeatureGateDoesNotReserve(t *testing.T) {
	svc, db := mockEcommerceAgentService(t)
	saveAdmissionFeatures(t, svc, defaultFeatureAvailability())
	req := mockEcommerceRequest()
	req.IdempotencyKey = "batch6-disabled"
	_, err := svc.GenerateEcommerceAgent(context.Background(), "owner", req, func(ecommerceagent.Event) error { return nil })
	var denied *AuthError
	if !errors.As(err, &denied) || denied.Status != 403 {
		t.Fatalf("disabled generation error=%v", err)
	}
	var runs, orders int64
	db.Model(&model.EcommerceAgentRun{}).Count(&runs)
	db.Model(&model.BillingOrder{}).Count(&orders)
	if runs != 0 || orders != 0 {
		t.Fatalf("disabled generation runs=%d orders=%d", runs, orders)
	}
	account, err := svc.repo.CreditAccount("owner")
	if err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 1_000_000 || account.ReservedMicrocredits != 0 {
		t.Fatalf("disabled generation account=%+v", account)
	}
}

func TestEcommerceGenerationEntryRejectsStaleDirectionBeforeBilling(t *testing.T) {
	svc, db := mockEcommerceAgentService(t)
	req := mockEcommerceRequest()
	req.IdempotencyKey = "batch6-stale-direction"
	req.SelectedRecommendationID = "missing"
	_, err := svc.GenerateEcommerceAgent(context.Background(), "owner", req, func(ecommerceagent.Event) error { return nil })
	var rejected *EcommerceAgentError
	if !errors.As(err, &rejected) || rejected.Code != "INVALID_INPUT" {
		t.Fatalf("stale direction error=%v", err)
	}
	var runs, orders int64
	db.Model(&model.EcommerceAgentRun{}).Count(&runs)
	db.Model(&model.BillingOrder{}).Count(&orders)
	if runs != 0 || orders != 0 {
		t.Fatalf("stale direction runs=%d orders=%d", runs, orders)
	}
}
