package repository

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
)

func agentWaitRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "waits.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	return New(db), db
}

// A durable user/browser wait committed by a tool must survive the Node
// bridge's generic waiting_tool/tool phase report.
func TestAgentPhaseReportPreservesDurableToolWaits(t *testing.T) {
	for _, kind := range []string{"subagents", "director_preview"} {
		t.Run(kind, func(t *testing.T) {
			r, db := agentWaitRepository(t)
			expires := time.Now().UTC().Add(time.Minute)
			if err := db.Create(&model.CloudAgentPiSession{ID: "s", UserID: "user", ConversationID: "s", ActiveRunID: "run", LeaseOwner: "worker", LeaseEpoch: 1, LeaseExpiresAt: &expires}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.CloudAgentExecution{ID: "run", UserID: "user", Status: "running", Engine: "pi", Revision: 1, LeaseOwner: "worker", LeaseExpiresAt: &expires, RuntimePhase: "waiting_tool", WaitKind: kind, WaitID: "durable-id", WaitReason: "durable-reason"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SetAgentPhase("user", "run", "worker", 1, "waiting_tool", "tool", "op", "等待工具结果"); err != nil {
				t.Fatal(err)
			}
			var run model.CloudAgentExecution
			if err := db.First(&run, "id = ?", "run").Error; err != nil {
				t.Fatal(err)
			}
			if run.WaitKind != kind || run.WaitID != "durable-id" || run.WaitReason != "durable-reason" {
				t.Fatalf("phase report replaced the %s wait: %+v", kind, run)
			}
			protected, err := r.WorkerRecoveryProtectedWait(run)
			if err != nil || !protected {
				t.Fatalf("%s must be a protected wait: %v %v", kind, protected, err)
			}
		})
	}
	r, _ := agentWaitRepository(t)
	if protected, err := r.WorkerRecoveryProtectedWait(model.CloudAgentExecution{UserID: "user", RuntimePhase: "waiting_tool", WaitKind: "tool", WaitID: "op"}); err != nil || protected {
		t.Fatalf("an ordinary tool wait without live tasks must not be protected: %v %v", protected, err)
	}
}
