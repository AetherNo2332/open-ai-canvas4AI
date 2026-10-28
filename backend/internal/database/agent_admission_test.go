package database_test

import (
	"fmt"
	"testing"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAgentAdmissionFairnessBeyondTwentyAndCanvasCap(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}); err != nil {
		t.Fatal(err)
	}
	seed := func(canvas string, n int) {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%03d", canvas, i)
			run := model.CloudAgentExecution{ID: id, UserID: "u", CanvasID: canvas, ConversationID: id, Engine: "pi", Status: "queued", CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond)}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.CloudAgentPiSession{ID: id, UserID: "u", ConversationID: id, ActiveRunID: id}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	seed("A", 100)
	seed("B", 2)
	seed("C", 2)
	p := model.DefaultAgentSchedulerSetting()
	p.MaxResidentPerCanvas = 2
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		run, err := repo.ClaimPiAgentFair(fmt.Sprintf("worker-%d", i), time.Now().Add(time.Minute), p)
		if err != nil || run == nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		seen[run.CanvasID]++
		if run.ID != fmt.Sprintf("%s-%03d", run.CanvasID, seen[run.CanvasID]-1) {
			t.Fatalf("not FIFO: %s", run.ID)
		}
		if i == 2 && (seen["A"] != 1 || seen["B"] != 1 || seen["C"] != 1) {
			t.Fatalf("not fair: %+v", seen)
		}
	}
	run, err := repo.ClaimPiAgentFair("extra", time.Now().Add(time.Minute), p)
	if err != nil || run != nil {
		t.Fatalf("canvas cap: %+v %v", run, err)
	}
	var waiting model.CloudAgentExecution
	db.First(&waiting, "id = ?", "A-002")
	if waiting.WaitKind != "canvas_capacity" {
		t.Fatalf("wait kind %s", waiting.WaitKind)
	}
	_ = repository.ErrCreationConflict
}

func TestAgentSchedulerConfigOptimisticAndAtomic(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	p := model.DefaultAgentSchedulerSetting()
	if err := repo.SaveAgentSchedulerSetting(&p, 0, &model.AdminAuditEvent{ID: "audit1"}); err != nil {
		t.Fatal(err)
	}
	if p.Revision != 1 {
		t.Fatalf("revision %d", p.Revision)
	}
	stale := model.DefaultAgentSchedulerSetting()
	if err := repo.SaveAgentSchedulerSetting(&stale, 0, &model.AdminAuditEvent{ID: "audit2"}); err != repository.ErrCreationConflict {
		t.Fatalf("stale: %v", err)
	}
	p.DispatchConcurrency = 8
	if err := repo.SaveAgentSchedulerSetting(&p, 1, &model.AdminAuditEvent{ID: "audit1"}); err == nil {
		t.Fatal("duplicate audit must roll back")
	}
	stored, err := repo.AgentSchedulerSetting(model.DefaultAgentSchedulerSetting())
	if err != nil || stored.Revision != 1 || stored.DispatchConcurrency != 4 {
		t.Fatalf("not atomic: %+v %v", stored, err)
	}
	var n int64
	db.Model(&model.AgentWakeEvent{}).Count(&n)
	if n != 1 {
		t.Fatalf("events %d", n)
	}
}

func TestAgentCanvasCapacityWaitIsNotUnavailableWorker(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentRuntimeInstance{ID: "live", Capacity: 64, Active: 16, Readiness: "ready", UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentExecution{ID: "waiting", UserID: "u", Engine: "pi", Status: "queued", WaitKind: "canvas_capacity", CreatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentExecution{ID: "user-waiting", UserID: "u", Engine: "pi", Status: "queued", WaitKind: "user_capacity", CreatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.AgentCapacityReport("live", model.AgentCapacityReport{Active: 0, Capacity: 64, Readiness: "ready"}); err != nil {
		t.Fatal(err)
	}
	var userWaiting model.CloudAgentExecution
	if err := db.First(&userWaiting, "id = ?", "user-waiting").Error; err != nil || userWaiting.WaitKind != "user_capacity" {
		t.Fatalf("capacity report rewrote protected wait: %+v %v", userWaiting, err)
	}
	runs, err := repo.UnclaimedPiAgentRuns(time.Now(), 20)
	if err != nil || len(runs) != 0 {
		t.Fatalf("valid capacity wait swept: %d %v", len(runs), err)
	}
}

func TestAgentUserAdmissionFairnessCapacityAndRecoverySQLite(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	seed := func(id, user, canvas, status string, expiry *time.Time) {
		t.Helper()
		if err := db.Create(&model.CloudAgentExecution{ID: id, UserID: user, CanvasID: canvas, ConversationID: id, Engine: "pi", Status: status, LeaseExpiresAt: expiry}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.CloudAgentPiSession{ID: model.PiSessionStorageID(user, id), UserID: user, ConversationID: id, ActiveRunID: id, LeaseExpiresAt: expiry}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed("u-a1", "u", "a", "queued", nil)
	seed("u-a2", "u", "a", "queued", nil)
	seed("u-b1", "u", "b", "queued", nil)
	seed("v-a1", "v", "a", "queued", nil)
	seed("v-a2", "v", "a", "queued", nil)
	seed("v-b1", "v", "b", "queued", nil)
	p := model.DefaultAgentSchedulerSetting()
	p.MaxResidentPerUser = 2
	p.MaxResidentPerCanvas = 2
	for i, want := range []string{"u-a1", "v-a1", "u-b1", "v-b1"} {
		run, err := repo.ClaimPiAgentFair(fmt.Sprintf("worker-%d", i), time.Now().UTC().Add(time.Minute), p)
		if err != nil || run == nil || run.ID != want {
			t.Fatalf("claim %d = %+v, %v; want %s", i, run, err, want)
		}
	}
	if run, err := repo.ClaimPiAgentFair("full", time.Now().UTC().Add(time.Minute), p); err != nil || run != nil {
		t.Fatalf("user cap admitted %+v: %v", run, err)
	}
	var waiting model.CloudAgentExecution
	if err := db.First(&waiting, "id = ?", "u-a2").Error; err != nil || waiting.WaitKind != "user_capacity" {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	protected, err := repo.UnclaimedPiAgentRuns(time.Now(), 10)
	if err != nil || len(protected) != 0 {
		t.Fatalf("capacity wait swept: %d %v", len(protected), err)
	}
	// A lower hot configuration drains naturally: existing leases stay valid while new claims wait.
	if err := db.Create(&model.AgentSchedulerSetting{ID: 1, DispatchConcurrency: 4, MaxResidentSessions: 64, MaxResidentPerUser: 2, MaxResidentPerCanvas: 2}).Error; err != nil {
		t.Fatal(err)
	}
	p.MaxResidentPerUser = 1
	if err := repo.SaveAgentSchedulerSetting(&p, 0, &model.AdminAuditEvent{ID: "user-cap-audit"}); err != nil {
		t.Fatal(err)
	}
	if run, err := repo.ClaimPiAgentFair("lowered", time.Now().UTC().Add(time.Minute), model.DefaultAgentSchedulerSetting()); err != nil || run != nil {
		t.Fatalf("lowered quota admitted %+v: %v", run, err)
	}
	// Terminal and expired leases leave the effective count without counter cleanup.
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", "u-a1").Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", "u-b1").Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	run, err := repo.ClaimPiAgentFair("recovered", time.Now().UTC().Add(time.Minute), model.DefaultAgentSchedulerSetting())
	if err != nil || run == nil || run.ID != "u-a2" {
		t.Fatalf("capacity recovery = %+v, %v", run, err)
	}
	if run.WaitKind != "" {
		t.Fatalf("wait did not clear: %+v", run)
	}
}

func TestAgentUserAdmissionSameOwnerNewEpochAndWaitingApprovalSQLite(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	for _, id := range []string{"reclaim", "next"} {
		canvas := "z"
		if id == "reclaim" {
			canvas = "a"
		}
		run := model.CloudAgentExecution{ID: id, UserID: "user", CanvasID: canvas, ConversationID: id, Engine: "pi", Status: "queued"}
		session := model.CloudAgentPiSession{ID: model.PiSessionStorageID("user", id), UserID: "user", ConversationID: id, ActiveRunID: id}
		if id == "reclaim" {
			run.Status, run.LeaseOwner, run.LeaseExpiresAt = "waiting_approval", "worker", &expired
			run.CreatedAt = time.Now().UTC().Add(-time.Hour)
			session.LeaseOwner, session.LeaseExpiresAt, session.LeaseEpoch = "worker", &expired, 7
		}
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := model.DefaultAgentSchedulerSetting()
	p.MaxResidentPerUser = 1
	claimed, err := repo.ClaimPiAgentFair("worker", time.Now().UTC().Add(time.Minute), p)
	if err != nil || claimed == nil || claimed.ID != "reclaim" {
		t.Fatalf("same owner reclaim = %+v, %v", claimed, err)
	}
	var session model.CloudAgentPiSession
	if err := db.First(&session, "active_run_id = ?", "reclaim").Error; err != nil || session.LeaseEpoch != 8 {
		t.Fatalf("new epoch = %+v, %v", session, err)
	}
	if run, err := repo.ClaimPiAgentFair("worker-2", time.Now().UTC().Add(time.Minute), p); err != nil || run != nil {
		t.Fatalf("waiting approval lease did not count: %+v, %v", run, err)
	}
	var next model.CloudAgentExecution
	if err := db.First(&next, "id = ?", "next").Error; err != nil || next.WaitKind != "user_capacity" {
		t.Fatalf("wait = %+v, %v", next, err)
	}
}
