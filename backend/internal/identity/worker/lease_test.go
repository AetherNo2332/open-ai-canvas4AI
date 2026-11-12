package worker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
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
	key := []byte(strings.Repeat("k", 48))
	repo, db, run := grantLeaseFixture(t)
	s := NewGrants(repo, key)
	rebindAuthTestLease(t, db, run.ID)
	if err := repo.ProvisionAgentWorker("worker-one", strings.Repeat("a", 48), "user"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ProvisionAgentWorker("worker-two", strings.Repeat("b", 48), "other"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ProvisionAgentWorker("worker-three", strings.Repeat("c", 48), "user"); err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", run.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	token, err := s.Issue("worker-one", authTestOwner, "user", run.ID, session.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	valid := func(userID, runID, owner string, epoch int64) bool {
		return s.Validate(token, userID, runID, owner, epoch)
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
	if _, err := s.Issue("worker-two", authTestOwner, "user", run.ID, session.LeaseEpoch); err == nil {
		t.Fatal("other tenant worker received grant")
	}
	if _, err := s.Issue("worker-three", authTestOwner, "user", run.ID, session.LeaseEpoch); err == nil {
		t.Fatal("same-tenant worker received another identity's lease grant")
	}
	parts := strings.Split(token, ".")
	var expired Grant
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(body, &expired)
	expired.Expires = time.Now().Add(-time.Minute).Unix()
	body, _ = json.Marshal(expired)
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	expiredToken := "run." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if s.Validate(expiredToken, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("expired grant accepted")
	}
	if err := repo.RevokeAgentWorker("worker-one"); err != nil {
		t.Fatal(err)
	}
	if valid("user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("revoked worker grant accepted")
	}
	if err := repo.SeedAgentWorker("worker-one", strings.Repeat("a", 48), "user"); err != nil {
		t.Fatal(err)
	}
	if valid("user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("restart seed reactivated revoked worker")
	}
}

func TestAgentRunGrantRejectsTerminalAndTakeover(t *testing.T) {
	key := []byte(strings.Repeat("k", 48))
	repo, db, run := grantLeaseFixture(t)
	s := NewGrants(repo, key)
	rebindAuthTestLease(t, db, run.ID)
	if err := repo.ProvisionAgentWorker("worker-one", strings.Repeat("a", 48), ""); err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", run.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	token, err := s.Issue("worker-one", authTestOwner, "user", run.ID, session.LeaseEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Update("lease_epoch", session.LeaseEpoch+1).Error; err != nil {
		t.Fatal(err)
	}
	if s.Validate(token, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("takeover accepted old epoch")
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Update("lease_epoch", session.LeaseEpoch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if s.Validate(token, "user", run.ID, authTestOwner, session.LeaseEpoch) {
		t.Fatal("terminal run accepted grant")
	}
}

type leaseRun struct {
	UpdatedAt      time.Time
	ID             string `gorm:"primaryKey"`
	UserID         string
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	Status         string
}
type leaseSession struct {
	UpdatedAt      time.Time
	ID             string `gorm:"primaryKey"`
	UserID         string
	ActiveRunID    string
	LeaseOwner     string
	LeaseEpoch     int64
	LeaseExpiresAt *time.Time
}

func grantLeaseFixture(t *testing.T) (*repository.Repository, *gorm.DB, leaseRun) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "grant.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	for _, table := range []struct {
		name  string
		model any
	}{{"agent_workers", &model.AgentWorker{}}, {"cloud_agent_executions", &leaseRun{}}, {"cloud_agent_pi_sessions", &leaseSession{}}} {
		if err := db.Table(table.name).AutoMigrate(table.model); err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().Add(time.Hour)
	run := leaseRun{ID: "run", UserID: "user", Status: "running", LeaseOwner: authTestOwner, LeaseExpiresAt: &expires}
	if err := db.Table("cloud_agent_executions").Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	session := leaseSession{ID: "session", UserID: "user", ActiveRunID: run.ID, LeaseOwner: authTestOwner, LeaseEpoch: 1, LeaseExpiresAt: &expires}
	if err := db.Table("cloud_agent_pi_sessions").Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	return repository.New(db), db, run
}
