package repository

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/model"
)

func messageQueueRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "messages.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}, &model.AgentConversationQueue{}, &model.AgentQueuedMessage{}, &model.AgentUISetting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "user", Username: "queue-user", Email: "queue@test.invalid"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentExecution{ID: "root", UserID: "user", CanvasID: "canvas", ConversationID: "root", Status: "running", Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	return New(db)
}

func TestAgentMessageQueueFIFOAndReplayAfterRemoval(t *testing.T) {
	r := messageQueueRepository(t)
	for _, id := range []string{"first", "second"} {
		err := r.WithAgentConversation("user", "root", func(tx *Repository, queue *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
			return tx.InsertAgentQueuedMessage(&model.AgentQueuedMessage{ID: id, UserID: "user", CanvasID: latest.CanvasID, ConversationID: "root", IdempotencyKey: id, Fingerprint: "original-" + id, RequestJSON: `{"prompt":"hello"}`, Prompt: "hello", Mode: "queue"}, queue, 20, 100)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.WithAgentConversation("other", "root", func(*Repository, *model.AgentConversationQueue, *model.CloudAgentExecution) error {
		t.Fatal("foreign scope opened")
		return nil
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign queue: %v", err)
	}
	if err := r.WithAgentConversation("user", "root", func(tx *Repository, queue *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		rows, err := tx.AgentQueuedMessages("user", "root")
		if err != nil {
			return err
		}
		if len(rows) != 2 || rows[0].ID != "first" || rows[1].Sequence <= rows[0].Sequence {
			t.Fatalf("FIFO: %+v", rows)
		}
		row := rows[0]
		if err := tx.FinishAgentQueuedMessage(&row, "cancelled", ""); err != nil {
			return err
		}
		return tx.SaveAgentConversationQueue(queue)
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.WithAgentConversation("user", "root", func(tx *Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		row := model.AgentQueuedMessage{ID: "replay", UserID: "user", ConversationID: "root", IdempotencyKey: "first", Fingerprint: "original-first", RequestJSON: `{"prompt":"hello"}`, Mode: "queue"}
		if err := tx.InsertAgentQueuedMessage(&row, q, 20, 100); err != nil {
			return err
		}
		if row.ID != "first" || row.Status != "cancelled" {
			t.Fatalf("cancelled message resurrected: %+v", row)
		}
		row.Fingerprint = "different"
		if err := tx.InsertAgentQueuedMessage(&row, q, 20, 100); !errors.Is(err, ErrCreationConflict) {
			t.Fatalf("changed replay: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentMessageQueueClaimRevisionAndPause(t *testing.T) {
	r := messageQueueRepository(t)
	err := r.WithAgentConversation("user", "root", func(tx *Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		row := model.AgentQueuedMessage{ID: "item", UserID: "user", CanvasID: "canvas", ConversationID: "root", IdempotencyKey: "item", Fingerprint: "body", RequestJSON: `{}`, Mode: "queue"}
		if err := tx.InsertAgentQueuedMessage(&row, q, 1, 1); err != nil {
			return err
		}
		other := row
		other.ID = "other"
		other.IdempotencyKey = "other"
		if err := tx.InsertAgentQueuedMessage(&other, q, 1, 1); !errors.Is(err, ErrAgentMessageQueueFull) {
			t.Fatalf("limit: %v", err)
		}
		if err := tx.ClaimAgentQueuedMessage(&row, "claim", "root", time.Now().Add(time.Minute)); err != nil {
			return err
		}
		stale := row
		stale.Revision--
		if err := tx.UpdateAgentQueuedMessageBody(&stale, `{"prompt":"changed"}`, "changed"); !errors.Is(err, ErrCreationConflict) {
			t.Fatalf("edit claimed: %v", err)
		}
		q.Paused = true
		q.PauseReason = "user_stop"
		return tx.SaveAgentConversationQueue(q)
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := r.AgentConversationQueue("user", "root")
	if err != nil || !q.Paused {
		t.Fatalf("pause was not durable: %+v %v", q, err)
	}
}

func TestAgentMessageQueueTracksCompletedQuestionAndBlocksOldAdmission(t *testing.T) {
	r := messageQueueRepository(t)
	if err := r.WithAgentConversation("user", "root", func(*Repository, *model.AgentConversationQueue, *model.CloudAgentExecution) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.MutateCloudAgent("user", "root", 1, func(run *model.CloudAgentExecution, _ *Repository) error {
		run.Status = "completed"
		run.EventCount = 1
		run.Journal = []model.CloudAgentEventRecord{{RunID: "root", UserID: "user", Sequence: 1, EventJSON: `{"type":"user_question","seq":1,"payload":{"question":"继续吗"}}`}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q, err := r.AgentConversationQueue("user", "root")
	if err != nil || q.AwaitingQuestionID != "root:1" {
		t.Fatalf("completed question lost: %+v %v", q, err)
	}
	if err := r.requireAgentMessageAdmission(&model.CloudAgentExecution{UserID: "user", ParentID: "root", ConversationID: "root", ID: "next"}); !errors.Is(err, ErrAgentMessageQuestionPending) {
		t.Fatalf("old admission bypassed question: %v", err)
	}
}
