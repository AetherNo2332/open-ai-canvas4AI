package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func queueTestInput(runID, key, mode string) AgentConversationMessageRequest {
	req := agentTestRequest()
	req.IdempotencyKey = key
	req.Prompt = key
	return AgentConversationMessageRequest{Mode: mode, ExpectedRunID: runID, Request: req}
}

func TestAgentMessageQueueStopInvalidatesPreparedAdmission(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	input := queueTestInput(root.ID, "queue-stop-preparation", "queue")
	receipt, err := s.SendAgentConversationMessage("user", root.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	finishConversationTestRun(t, db, root.ID)
	var message model.AgentQueuedMessage
	err = s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage("user", root.ID, receipt.MessageID)
		if err != nil {
			return err
		}
		if err = tx.ClaimAgentQueuedMessage(row, "test-claim", latest.ID, time.Now().Add(time.Minute)); err != nil {
			return err
		}
		message = *row
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var admission repository.CloudAgentAdmission
	if _, err = s.createCloudAgentRunScoped("user", input.Request, root.ID, &cloudAgentRunScope{Prepared: &admission}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.repo.CreditAccount("user")
	if err = s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
		t.Fatal(err)
	}
	policy, _ := s.RuntimePolicy()
	if err = s.repo.CommitAgentMessageAdmission(message, admission, policy.Task.ActiveTaskLimit, policy.Resource.TaskCount, gigabytes(policy.Resource.TaskDataGB)); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("prepared run escaped stop: %v", err)
	}
	after, _ := s.repo.CreditAccount("user")
	if after.ReservedMicrocredits != before.ReservedMicrocredits {
		t.Fatalf("rejected admission charged: %+v => %+v", before, after)
	}
	var count int64
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 1 {
		t.Fatalf("rejected admission committed: %d", count)
	}
	view, _ := s.AgentMessageQueue("user", root.ID)
	if !view.Paused || len(view.Items) != 1 || view.Items[0].Status != "queued" {
		t.Fatalf("stopped queue lost: %+v", view)
	}
}

func TestAgentMessageQueueConcurrentDispatchAndRemovedReplay(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	for _, key := range []string{"queue-race-first", "queue-race-second"} {
		if _, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, key, "queue")); err != nil {
			t.Fatal(err)
		}
	}
	finishConversationTestRun(t, db, root.ID)
	other := &Service{repo: repository.New(db), dataDir: s.dataDir}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, svc := range []*Service{s, other} {
		wg.Add(1)
		go func(svc *Service) { defer wg.Done(); _, err := svc.DispatchAgentMessages(10); errs <- err }(svc)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 2 {
		t.Fatalf("concurrent duplicate run: %d", count)
	}
	view, _ := s.AgentMessageQueue("user", root.ID)
	if len(view.Items) != 1 {
		t.Fatalf("concurrent queue: %+v", view)
	}
	row := view.Items[0]
	if _, err := s.CancelAgentQueuedMessage("user", root.ID, row.ID, row.Revision); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "queue-race-second", "queue"))
	if err != nil || replayed.Delivery != "cancelled" {
		t.Fatalf("removed message resurrected: %+v %v", replayed, err)
	}
}

func TestAgentMessageQueueSequentialAndIdempotent(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	first, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "queue-first-message", "queue"))
	if err != nil || first.Delivery != "queued" {
		t.Fatalf("enqueue: %+v %v", first, err)
	}
	second, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "queue-second-message", "queue"))
	if err != nil || second.Delivery != "queued" {
		t.Fatalf("second: %+v %v", second, err)
	}
	var count int64
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 1 {
		t.Fatalf("enqueue started a billed run: %d", count)
	}
	finishConversationTestRun(t, db, root.ID)
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 1 {
		t.Fatalf("dispatch: %d %v", n, err)
	}
	view, err := s.AgentMessageQueue("user", root.ID)
	if err != nil || len(view.Items) != 1 || view.Items[0].ID != second.MessageID || view.LatestRunID == root.ID {
		t.Fatalf("FIFO: %+v %v", view, err)
	}
	replay, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "queue-first-message", "queue"))
	if err != nil || replay.RunID != view.LatestRunID || replay.Delivery != "started" {
		t.Fatalf("unknown replay: %+v %v", replay, err)
	}
	finishConversationTestRun(t, db, view.LatestRunID)
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 1 {
		t.Fatalf("second dispatch: %d %v", n, err)
	}
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 3 {
		t.Fatalf("run count: %d", count)
	}
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 0 {
		t.Fatalf("duplicate dispatch: %d %v", n, err)
	}
}

func TestAgentMessageQueueQuestionRequiresExplicitAnswer(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	input := queueTestInput(root.ID, "queue-do-not-answer", "queue")
	input.Request.Prompt = "确认"
	if _, err := s.SendAgentConversationMessage("user", root.ID, input); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	state.event(root.ID, "user_question", map[string]any{"question": "采用这个方案吗", "options": []any{map[string]any{"label": "确认"}, map[string]any{"label": "取消"}}})
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "completed"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 0 {
		t.Fatalf("queued answer bypassed question: %d %v", n, err)
	}
	view, err := s.AgentMessageQueue("user", root.ID)
	if err != nil || view.AwaitingQuestionID == "" {
		t.Fatalf("question: %+v %v", view, err)
	}
	if _, err := s.CreateCloudAgentRun("user", input.Request, root.ID); err == nil {
		t.Fatal("old endpoint bypassed question")
	}
	answer := queueTestInput(root.ID, "explicit-user-answer", "interject")
	answer.ReplyToQuestionID = view.AwaitingQuestionID
	response, err := s.SendAgentConversationMessage("user", root.ID, answer)
	if err != nil || response.Delivery != "started" {
		t.Fatalf("explicit answer: %+v %v", response, err)
	}
	finishConversationTestRun(t, db, response.RunID)
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 1 {
		t.Fatalf("queue did not resume: %d %v", n, err)
	}
}

func TestAgentMessageQueueEditingPromotionStopAndSettings(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	input := queueTestInput(root.ID, "queue-edit-message", "queue")
	item, err := s.SendAgentConversationMessage("user", root.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := s.AgentMessageQueue("user", root.ID)
	text := "修改后的消息"
	if _, err := s.UpdateAgentQueuedMessage("user", root.ID, item.MessageID, AgentQueuedMessageUpdate{Revision: view.Items[0].Revision, Prompt: &text}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentQueuedMessage("user", root.ID, item.MessageID, AgentQueuedMessageUpdate{Revision: view.Items[0].Revision, Action: "interject"}); err == nil {
		t.Fatal("stale edit accepted")
	}
	view, _ = s.AgentMessageQueue("user", root.ID)
	promoted, err := s.UpdateAgentQueuedMessage("user", root.ID, item.MessageID, AgentQueuedMessageUpdate{Revision: view.Items[0].Revision, Action: "interject"})
	if err != nil || promoted.Delivery != "interjected" {
		t.Fatalf("promotion: %+v %v", promoted, err)
	}
	_, state := agentInterjectionState(t, s, root.ID)
	if len(state.PendingInterjections) != 1 || state.PendingInterjections[0].Text != text {
		t.Fatalf("promoted body: %+v", state.PendingInterjections)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
		t.Fatal(err)
	}
	view, err = s.AgentMessageQueue("user", root.ID)
	if err != nil || !view.Paused {
		t.Fatalf("stop did not pause: %+v %v", view, err)
	}
	settings, err := s.AgentUISettings("user")
	if err != nil || settings.DefaultSendMode != "interject" {
		t.Fatalf("default: %+v %v", settings, err)
	}
	settings.DefaultSendMode = "queue"
	if _, err = s.UpdateAgentUISettings("user", *settings); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateAgentUISettings("user", *settings); err == nil {
		t.Fatal("stale settings accepted")
	}
	if _, err = s.AgentMessageQueue("other", root.ID); err == nil {
		t.Fatal("foreign queue exposed")
	}
	if errors.Is(err, repository.ErrCreationConflict) {
		t.Fatal("ownership leaked as conflict")
	}
}
