package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

const authTestOwner = "worker-one:slot"

func rebindAuthTestLease(t *testing.T, db *gorm.DB, runID string) {
	t.Helper()
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", runID).Update("lease_owner", authTestOwner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("active_run_id = ?", runID).Update("lease_owner", authTestOwner).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunGrantBoundToLiveLeaseAndWorker(t *testing.T) {
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("k", 48))
	s, db, run := piAgentTestLeasedFixture(t)
	rebindAuthTestLease(t, db, run.ID)
	if err := s.repo.ProvisionAgentWorker("worker-one", strings.Repeat("a", 48), "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ProvisionAgentWorker("worker-two", strings.Repeat("b", 48), "other"); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ProvisionAgentWorker("worker-three", strings.Repeat("c", 48), "user"); err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", run.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueAgentRunGrant("worker-one", authTestOwner, "user", run.ID, session.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	valid := func(userID, runID, owner string, epoch int64) bool {
		return s.ValidateAgentRunGrant(token, userID, runID, owner, epoch)
	}
	if !valid("user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("live grant rejected")
	}
	for _, bad := range []struct {
		user, run, owner string
		epoch            int64
	}{
		{"other", run.ID, authTestOwner, session.LeaseEpoch},
		{"user", "another-run", authTestOwner, session.LeaseEpoch},
		{"user", run.ID, "worker-b", session.LeaseEpoch},
		{"user", run.ID, authTestOwner, session.LeaseEpoch + 1},
	} {
		if valid(bad.user, bad.run, bad.owner, bad.epoch) {
			t.Fatalf("grant escaped binding: %+v", bad)
		}
	}
	if _, err := s.IssueAgentRunGrant("worker-two", authTestOwner, "user", run.ID, session.LeaseEpoch); err == nil {
		t.Fatal("other tenant worker received grant")
	}
	if _, err := s.IssueAgentRunGrant("worker-three", authTestOwner, "user", run.ID, session.LeaseEpoch); err == nil {
		t.Fatal("same-tenant worker received another identity's lease grant")
	}
	parts := strings.Split(token, ".")
	var expired AgentRunGrant
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(body, &expired)
	expired.Expires = time.Now().Add(-time.Minute).Unix()
	body, _ = json.Marshal(expired)
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, agentGrantKey())
	mac.Write([]byte(payload))
	expiredToken := "run." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if s.ValidateAgentRunGrant(expiredToken, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("expired grant accepted")
	}
	if err := s.repo.RevokeAgentWorker("worker-one"); err != nil {
		t.Fatal(err)
	}
	if valid("user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("revoked worker grant accepted")
	}
	if err := s.repo.SeedAgentWorker("worker-one", strings.Repeat("a", 48), "user"); err != nil {
		t.Fatal(err)
	}
	if valid("user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("restart seed reactivated revoked worker")
	}
}

func TestAgentRunGrantRejectsTerminalAndTakeover(t *testing.T) {
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("k", 48))
	s, db, run := piAgentTestLeasedFixture(t)
	rebindAuthTestLease(t, db, run.ID)
	if err := s.repo.ProvisionAgentWorker("worker-one", strings.Repeat("a", 48), ""); err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", run.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueAgentRunGrant("worker-one", authTestOwner, "user", run.ID, session.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Update("lease_epoch", session.LeaseEpoch+1).Error; err != nil {
		t.Fatal(err)
	}
	if s.ValidateAgentRunGrant(token, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("takeover accepted old epoch")
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Update("lease_epoch", session.LeaseEpoch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if s.ValidateAgentRunGrant(token, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("terminal run accepted grant")
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
