package app

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestAgentAvailabilityDistinguishesConfigurationCapacityAndReadiness(t *testing.T) {
	s, db, _ := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "")
	status, err := s.AgentAvailability()
	if err != nil || status.Configured || status.QueueReason != "not_configured" {
		t.Fatalf("unconfigured: %+v %v", status, err)
	}
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "test-token")
	status, err = s.AgentAvailability()
	if err != nil || status.QueueReason != "worker_offline" {
		t.Fatalf("offline: %+v %v", status, err)
	}
	if err = s.PiCapacityReport("worker", model.AgentCapacityReport{Capacity: 1, Readiness: "degraded", ReadinessReason: "protocol mismatch"}); err != nil {
		t.Fatal(err)
	}
	status, err = s.AgentAvailability()
	if err != nil || status.QueueReason != "worker_not_ready" {
		t.Fatalf("degraded: %+v %v", status, err)
	}
	if err = s.PiCapacityReport("worker", model.AgentCapacityReport{Active: 1, Capacity: 1, Readiness: "ready", AppliedConfigRevision: 0}); err != nil {
		t.Fatal(err)
	}
	status, err = s.AgentAvailability()
	if err != nil || status.ReadyWorkers != 1 || status.QueueReason != "capacity" {
		t.Fatalf("full: %+v %v", status, err)
	}
	if err = s.PiCapacityReport("worker", model.AgentCapacityReport{Capacity: 1, Readiness: "ready", AppliedConfigRevision: 0}); err != nil {
		t.Fatal(err)
	}
	status, err = s.AgentAvailability()
	if err != nil || status.ReadyWorkers != 1 || status.QueueReason != "" {
		t.Fatalf("recovered: %+v %v", status, err)
	}
}

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
	if _, err = s.UpdateAgentSchedulerSetting(admin, AgentSchedulerUpdate{ExpectedRevision: p.Revision, DispatchConcurrency: 4, MaxResidentSessions: 16, MaxResidentPerUser: 16, MaxResidentPerCanvas: 16}); err != nil {
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
