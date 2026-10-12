package app

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestAgentSchedulerAdminPolicyAndCapacityDraining(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AdminAuditEvent{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
	if _, err := s.AdminAgentSchedulerSetting(&model.User{ID: "user"}); err == nil {
		t.Fatal("non-admin read accepted")
	}
	p, err := s.AdminAgentSchedulerSetting(admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateAgentSchedulerSetting(admin, AgentSchedulerUpdate{ExpectedRevision: p.Revision, DispatchConcurrency: 4, MaxResidentSessions: 16, MaxResidentPerCanvas: 16}); err != nil {
		t.Fatal(err)
	}
	if err = s.PiCapacityReport("executor", model.AgentCapacityReport{Active: 32, Capacity: 16, Draining: true, AppliedConfigRevision: 1}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.AdminAgentSchedulerStatus(admin)
	if err != nil || len(rows) != 1 || !rows[0].Online || !rows[0].Draining {
		t.Fatalf("status: %+v %v", rows, err)
	}
}
