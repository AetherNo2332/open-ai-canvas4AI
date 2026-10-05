package app

import (
	"context"
	"infinite-canvas/backend/internal/model"
	"testing"
	"time"
)

func TestCrewCancelIsIdempotentAndAggregatesUsage(t *testing.T) {
	s, crew, _ := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "cancel", IdempotencyKey: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.CrewBudgetUsage("user", run.ID)
	if err != nil || usage.MaxConcurrentMembers != 2 || usage.ActiveMembers != 1 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	if err := s.CancelCrewRun(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCrewRun(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.GetCrewRun("user", run.ID)
	if err != nil || view.Status != model.CrewRunCancelled {
		t.Fatalf("cancel: %+v %v", view, err)
	}
	for _, member := range view.Members {
		if member.Status != model.MemberRunCancelled && member.Status != model.MemberRunCompleted {
			t.Fatalf("member not cancelled: %+v", member)
		}
	}
}

func TestCrewRecoveryFailsExpiredMemberWithoutTouchingCompleted(t *testing.T) {
	s, crew, db := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "recover", IdempotencyKey: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	member := run.Members[1]
	expired := time.Now().Add(-time.Minute)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", member.AgentRunID).Updates(map[string]any{"lease_expires_at": expired, "status": "running"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverCrewRuns(time.Now()); err != nil {
		t.Fatal(err)
	}
	execution, _ := s.repo.CloudAgent("user", member.AgentRunID)
	if execution.Status != "failed" {
		t.Fatal("expired member not failed")
	}
	if err := s.RecoverCrewRuns(time.Now()); err != nil {
		t.Fatal(err)
	}
	view, _ := s.GetCrewRun("user", run.ID)
	if view.Members[1].Status != model.MemberRunFailed {
		t.Fatal("member state not projected")
	}
}
