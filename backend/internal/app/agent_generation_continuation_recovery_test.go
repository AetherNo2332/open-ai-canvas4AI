package app

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// A fresh connection and Service discard process-local repository and policy state.
func reopenGenerationContinuationService(t *testing.T, s *Service, db *gorm.DB) (*Service, *gorm.DB) {
	t.Helper()
	var files []struct{ File string }
	if err := db.Raw("PRAGMA database_list").Scan(&files).Error; err != nil || len(files) == 0 {
		t.Fatalf("database file: %v %v", files, err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Open(files[0].File+"?_journal_mode=WAL&_busy_timeout=15000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	freshConn, err := reopened.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = freshConn.Close() })
	return &Service{repo: repository.New(reopened), dataDir: s.dataDir}, reopened
}

func TestGenerationContinuationRestartExpiredLeaseAndCompetingInstances(t *testing.T) {
	s, db, source, batch := readyGenerationContinuationFixture(t)
	s, db = reopenGenerationContinuationService(t, s, db)
	if n, err := s.QueueReadyGenerationContinuations(20); err != nil || n != 1 {
		t.Fatalf("ready restart enqueue: %d %v", n, err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	var claimed model.AgentQueuedMessage
	err := s.repo.WithAgentConversation("user", batch.ConversationID, func(tx *repository.Repository, _ *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessageForDispatch("user", batch.ConversationID)
		if err != nil {
			return err
		}
		if row == nil {
			return errors.New("missing continuation")
		}
		if err = tx.ClaimAgentQueuedMessage(row, "crashed-dispatcher", latest.ID, time.Now().Add(time.Minute)); err != nil {
			return err
		}
		claimed = *row
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var request CloudAgentRequest
	if err = json.Unmarshal([]byte(claimed.RequestJSON), &request); err != nil {
		t.Fatal(err)
	}
	var stale repository.CloudAgentAdmission
	if _, err = s.createCloudAgentRunScoped("user", request, source.ID, &cloudAgentRunScope{Prepared: &stale}); err != nil {
		t.Fatal(err)
	}
	before, err := s.repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.DispatchAgentMessages(20); err != nil || n != 0 {
		t.Fatalf("unexpired lease stolen: %d %v", n, err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	if err := db.Model(&model.AgentQueuedMessage{}).Where("id = ?", claimed.ID).Update("lease_until", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	var files []struct{ File string }
	if err := db.Raw("PRAGMA database_list").Scan(&files).Error; err != nil || len(files) == 0 {
		t.Fatalf("peer database file: %v %v", files, err)
	}
	peerDB, err := gorm.Open(sqlite.Open(files[0].File+"?_journal_mode=WAL&_busy_timeout=15000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	peerConn, err := peerDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peer := &Service{repo: repository.New(peerDB), dataDir: s.dataDir}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, instance := range []*Service{s, peer} {
		wg.Add(1)
		go func(instance *Service) { defer wg.Done(); _, err := instance.DispatchAgentMessages(20); errs <- err }(instance)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.repo.CommitAgentMessageAdmission(claimed, stale, policy.Task.ActiveTaskLimit, policy.Resource.TaskCount, gigabytes(policy.Resource.TaskDataGB)); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("stale lease committed admission: %v", err)
	}
	stored, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.AgentGenerationBatchConsumed || stored.ContinuationRunID == "" {
		message, _ := s.repo.AgentQueuedMessage("user", batch.ConversationID, claimed.ID)
		queue, _ := s.repo.AgentConversationQueue("user", batch.ConversationID)
		candidates, scanErr := s.repo.PendingAgentMessageConversations(20)
		t.Fatalf("not consumed: %+v message=%+v queue=%+v candidates=%+v scanErr=%v enabled=%v", stored, message, queue, candidates, scanErr, s.repo.HasAgentMessageQueue())
	}
	after, err := s.repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Order == nil || after.ReservedMicrocredits-before.ReservedMicrocredits != stale.Order.ReservedAmountMicrocredits {
		t.Fatalf("expected exactly one ordinary run reservation: %+v => %+v order=%+v", before, after, stale.Order)
	}
	var beforeOrders int64
	if err := db.Model(&model.BillingOrder{}).Count(&beforeOrders).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := peer.SettlePendingCloudAgentGenerations(); err != nil {
			t.Fatal(err)
		}
		if _, err := peer.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
	}
	var messages, runs, orders int64
	if err := db.Model(&model.AgentQueuedMessage{}).Where("kind = ?", model.AgentQueuedMessageGenerationContinuation).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	replayed, err := s.repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	if messages != 1 || runs != 2 || orders != beforeOrders || replayed.ReservedMicrocredits != after.ReservedMicrocredits || replayed.AvailableMicrocredits != after.AvailableMicrocredits {
		t.Fatalf("duplicate admission: messages=%d runs=%d orders=%d/%d account=%+v/%+v", messages, runs, orders, beforeOrders, after, replayed)
	}
}

func TestGenerationContinuationAdmissionFailureDoesNotPauseUserQueue(t *testing.T) {
	s, db, source, batch := readyGenerationContinuationFixture(t)
	if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", "user").Update("available_microcredits", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchAgentMessages(20); err != nil {
		t.Fatal(err)
	}
	queue, err := s.AgentMessageQueue("user", batch.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Paused {
		t.Fatalf("internal failure paused user messages: %+v", queue)
	}
	stored, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.repo.AgentQueuedMessage("user", batch.ConversationID, stored.QueuedMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.AgentGenerationBatchQueued || message.LastError == "" {
		t.Fatalf("failure lost: %+v %+v", stored, message)
	}
	for range 3 {
		if _, err := s.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
	}
	still, err := s.repo.AgentQueuedMessage("user", batch.ConversationID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Attempts != message.Attempts {
		t.Fatalf("deterministic admission failure retried: %d => %d", message.Attempts, still.Attempts)
	}
	if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", "user").Update("available_microcredits", 10000).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendAgentConversationMessage("user", batch.ConversationID, queueTestInput(source.ID, "user-after-internal-failure", "queue")); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.LatestAgentConversationRun("user", batch.ConversationID)
	if err != nil || latest.ID == source.ID {
		t.Fatalf("user message blocked after internal failure: %+v %v", latest, err)
	}
}
