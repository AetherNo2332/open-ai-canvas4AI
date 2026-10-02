package database_test

import (
	"fmt"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
	"time"
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
	if err := db.Create(&model.AgentRuntimeInstance{ID: "live", Capacity: 64, Active: 16, UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentExecution{ID: "waiting", UserID: "u", Engine: "pi", Status: "queued", WaitKind: "canvas_capacity", CreatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	runs, err := repo.UnclaimedPiAgentRuns(time.Now(), 20)
	if err != nil || len(runs) != 0 {
		t.Fatalf("valid capacity wait swept: %d %v", len(runs), err)
	}
}
