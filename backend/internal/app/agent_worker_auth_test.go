package app

import (
	"strings"
	"testing"
	"yingce/backend/internal/model"
)

func TestAgentGrantAdapterAfterClaim(t *testing.T) {
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("k", 48))
	s, db, run := piAgentTestLeasedFixture(t)
	const owner = "worker-one:slot"
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("lease_owner", owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("active_run_id = ?", run.ID).Update("lease_owner", owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ProvisionAgentWorker("worker-one", strings.Repeat("s", 48), "user"); err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", run.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueAgentRunGrant("worker-one", owner, "user", run.ID, session.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if !s.ValidateAgentRunGrant(token, "user", run.ID, owner, session.LeaseEpoch) {
		t.Fatal("grant adapter rejected live claim")
	}
	if err = s.repo.RevokeAgentWorker("worker-one"); err != nil {
		t.Fatal(err)
	}
	if s.ValidateAgentRunGrant(token, "user", run.ID, owner, session.LeaseEpoch) {
		t.Fatal("grant adapter bypassed revocation")
	}
}
func TestAgentWorkerScopeFiltersBeforeClaim(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	if claim, err := s.ClaimPiAgentForWorker("scoped-worker", "other-user"); err != nil || claim != nil {
		t.Fatalf("other tenant claim = %#v, %v", claim, err)
	}
	var current model.CloudAgentExecution
	if err := db.First(&current, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.LeaseOwner != "" {
		t.Fatalf("unauthorized claim modified lease: %q", current.LeaseOwner)
	}
	if claim, err := s.ClaimPiAgentForWorker("scoped-worker", "user"); err != nil || claim == nil || claim.RunID != run.ID {
		t.Fatalf("authorized claim = %#v, %v", claim, err)
	}
}
