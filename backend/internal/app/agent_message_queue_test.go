package app

import (
	"context"
	"encoding/json"
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

func TestAgentMessageQueueRejectsASecondPendingAnswer(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	finishConversationTestRun(t, db, root.ID)
	first := queueTestInput(root.ID, "first-pending-answer", "interject")
	first.ReplyToQuestionID = root.ID + ":1"
	raw, _ := json.Marshal(first.Request)
	if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		q.AwaitingQuestionID = first.ReplyToQuestionID
		q.QuestionRunID = root.ID
		return tx.InsertAgentQueuedMessage(&model.AgentQueuedMessage{ID: "first-answer", UserID: "user", ConversationID: root.ID, CanvasID: "agent-canvas", IdempotencyKey: first.Request.IdempotencyKey, Fingerprint: "original", Mode: "interject", Prompt: first.Request.Prompt, RequestJSON: string(raw), ReplyToQuestionID: first.ReplyToQuestionID}, q, 20, 100)
	}); err != nil {
		t.Fatal(err)
	}
	second := queueTestInput(root.ID, "second-pending-answer", "interject")
	second.ReplyToQuestionID = first.ReplyToQuestionID
	if _, err := s.SendAgentConversationMessage("user", root.ID, second); err == nil {
		t.Fatal("two devices queued two answers to one question")
	}
}

func TestAgentMessageQueueFullStillAllowsAnInterjection(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	policy, _ := s.RuntimePolicy()
	policy.Task.AgentMaxQueuedMessagesPerConversation = 1
	if _, err := s.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	actual, _ := s.RuntimePolicy()
	if actual.Task.AgentMaxQueuedMessagesPerConversation != 1 {
		t.Fatal("test policy was not applied")
	}
	if _, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "fill-one-queue-slot", "queue")); err != nil {
		t.Fatal(err)
	}
	response, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "correct-active-agent", "interject"))
	if err != nil || response.Delivery != "interjected" {
		t.Fatalf("full queue blocked steering: %+v %v", response, err)
	}
}

func TestAgentMessageQueueFailureTextIsActionableAndPrivate(t *testing.T) {
	if got := agentMessageFailureText(repository.ErrInsufficientCredits); got != "积分不足，请补充额度" {
		t.Fatalf("credit guidance: %q", got)
	}
	if got := agentMessageFailureText(errors.New("SQL connection contains private file path")); got != "暂时无法发送，请稍后重试" {
		t.Fatalf("private storage detail exposed: %q", got)
	}
}

func TestAgentMessageQueueWaitingConversationDoesNotStarveReadyOne(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	if _, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "first-active-queue", "queue")); err != nil {
		t.Fatal(err)
	}
	request := agentTestRequest()
	request.IdempotencyKey = "other-conversation-root"
	other, err := s.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendAgentConversationMessage("user", other.ID, queueTestInput(other.ID, "other-ready-queue", "queue")); err != nil {
		t.Fatal(err)
	}
	finishConversationTestRun(t, db, other.ID)
	if n, err := s.DispatchAgentMessages(1); err != nil || n != 1 {
		t.Fatalf("waiting conversation starved ready queue: %d %v", n, err)
	}
}

func TestAgentMessageQueueRecoversExplicitAnswerAheadOfOrdinaryMessages(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	if _, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "ordinary-message-before-question", "queue")); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	state.event(root.ID, "user_question", map[string]any{"question": "继续吗", "options": []any{map[string]any{"label": "继续"}, map[string]any{"label": "停止"}}})
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "completed"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	view, _ := s.AgentMessageQueue("user", root.ID)
	answer := queueTestInput(root.ID, "crash-recovery-answer", "interject")
	raw, _ := json.Marshal(answer.Request)
	if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		return tx.InsertAgentQueuedMessage(&model.AgentQueuedMessage{ID: "recovered-answer", UserID: "user", CanvasID: "agent-canvas", ConversationID: root.ID, IdempotencyKey: answer.Request.IdempotencyKey, Fingerprint: "original", RequestJSON: string(raw), Prompt: answer.Request.Prompt, Mode: "interject", ReplyToQuestionID: view.AwaitingQuestionID}, q, 20, 100)
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DispatchAgentMessages(1); err != nil || n != 1 {
		t.Fatalf("answer stranded after restart: %d %v", n, err)
	}
	view, err := s.AgentMessageQueue("user", root.ID)
	if err != nil || view.AwaitingQuestionID != "" || len(view.Items) != 1 || view.Items[0].Prompt != "ordinary-message-before-question" {
		t.Fatalf("answer recovery changed ordinary queue: %+v %v", view, err)
	}
}

func TestAgentMessageQueuePromotionSurvivesBackgroundRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		paused    bool
		transient bool
	}{
		{name: "worker_before_frontend_dispatch"},
		{name: "restart_with_paused_queue", paused: true},
		{name: "retry_after_temporary_admission_failure", transient: true},
		{name: "paused_retry_after_temporary_admission_failure", paused: true, transient: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, db, root := reliableAgentRoot(t)
			var promotedID string
			for _, key := range []string{"ordinary-first", "promoted-second", "ordinary-third"} {
				receipt, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, key, "queue"))
				if err != nil {
					t.Fatal(err)
				}
				if key == "promoted-second" {
					promotedID = receipt.MessageID
				}
			}
			finishConversationTestRun(t, db, root.ID)
			// Stop at the real promotion transaction boundary: the HTTP caller
			// has not yet reached its opportunistic dispatch call.
			if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
				row, err := tx.AgentQueuedMessage("user", root.ID, promotedID)
				if err != nil {
					return err
				}
				if err = tx.PromoteAgentQueuedMessage(row); err != nil {
					return err
				}
				q.Paused = scenario.paused
				return tx.SaveAgentConversationQueue(q)
			}); err != nil {
				t.Fatal(err)
			}
			if scenario.transient {
				var claimed model.AgentQueuedMessage
				if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, _ *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
					row, err := tx.AgentQueuedMessage("user", root.ID, promotedID)
					if err != nil {
						return err
					}
					if err = tx.ClaimAgentQueuedMessage(row, "priority-retry", latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
						return err
					}
					claimed = *row
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.recoverAgentMessageClaim(claimed, repository.ErrActiveTaskLimit); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&model.AgentQueuedMessage{}).Where("id = ?", promotedID).Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
					t.Fatal(err)
				}
			}
			other := &Service{repo: repository.New(db), dataDir: s.dataDir}
			if n, err := other.DispatchAgentMessages(1); err != nil || n != 1 {
				t.Fatalf("persisted promotion was stranded: %d %v", n, err)
			}
			promoted, err := other.repo.AgentQueuedMessage("user", root.ID, promotedID)
			if err != nil || promoted.Status != "started" {
				t.Fatalf("background dispatch lost promotion priority: %+v %v", promoted, err)
			}
			view, err := other.AgentMessageQueue("user", root.ID)
			if err != nil || len(view.Items) != 2 || view.Items[0].Prompt != "ordinary-first" || view.Items[1].Prompt != "ordinary-third" || view.Paused != scenario.paused {
				t.Fatalf("promotion changed ordinary queue: %+v %v", view, err)
			}
			finishConversationTestRun(t, db, promoted.RunID)
			if scenario.paused {
				if n, err := other.DispatchAgentMessages(1); err != nil || n != 0 {
					t.Fatalf("explicit recovery released the paused ordinary queue: %d %v", n, err)
				}
			}
		})
	}
}

func TestAgentMessageQueueExplicitRecoveryRespectsLaterPause(t *testing.T) {
	for _, action := range []string{"stop", "pause", "definitive_failure"} {
		t.Run(action, func(t *testing.T) {
			s, db, root := reliableAgentRoot(t)
			if _, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, "ordinary-paused", "queue")); err != nil {
				t.Fatal(err)
			}
			finishConversationTestRun(t, db, root.ID)
			input := queueTestInput(root.ID, "explicit-recovery", "interject")
			raw, _ := json.Marshal(input.Request)
			if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
				q.Paused = true
				return tx.InsertAgentQueuedMessage(&model.AgentQueuedMessage{ID: "explicit-recovery", UserID: "user", CanvasID: "agent-canvas", ConversationID: root.ID, IdempotencyKey: input.Request.IdempotencyKey, Fingerprint: "original", RequestJSON: string(raw), Prompt: input.Request.Prompt, Mode: "interject"}, q, 20, 100)
			}); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "stop":
				if err := s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
					t.Fatal(err)
				}
			case "pause":
				view, _ := s.AgentMessageQueue("user", root.ID)
				if _, err := s.UpdateAgentMessageQueue("user", root.ID, AgentMessageQueueUpdate{Revision: view.Revision, Action: "pause"}); err != nil {
					t.Fatal(err)
				}
			case "definitive_failure":
				var claimed model.AgentQueuedMessage
				if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, _ *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
					row, err := tx.AgentQueuedMessage("user", root.ID, "explicit-recovery")
					if err != nil {
						return err
					}
					if err = tx.ClaimAgentQueuedMessage(row, "rejected-recovery", latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
						return err
					}
					claimed = *row
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.recoverAgentMessageClaim(claimed, repository.ErrInsufficientCredits); err != nil {
					t.Fatal(err)
				}
			}
			other := &Service{repo: repository.New(db), dataDir: s.dataDir}
			if n, err := other.DispatchAgentMessages(1); err != nil || n != 0 {
				t.Fatalf("explicit dispatch escaped a later pause: %d %v", n, err)
			}
			view, err := other.AgentMessageQueue("user", root.ID)
			if err != nil || !view.Paused || len(view.Items) != 2 {
				t.Fatalf("pause lost messages: %+v %v", view, err)
			}
			if _, err = other.UpdateAgentMessageQueue("user", root.ID, AgentMessageQueueUpdate{Revision: view.Revision, Action: "resume"}); err != nil {
				t.Fatal(err)
			}
			if n, err := other.DispatchAgentMessages(1); err != nil || n != 1 {
				t.Fatalf("manual resume did not recover the message: %d %v", n, err)
			}
			recovery, err := other.repo.AgentQueuedMessage("user", root.ID, "explicit-recovery")
			if err != nil || recovery.Status != "started" {
				t.Fatalf("manual resume lost priority: %+v %v", recovery, err)
			}
		})
	}
}

func TestAgentMessageQueueNewFailureRevokesEarlierExplicitDispatches(t *testing.T) {
	for _, source := range []string{"observed_terminal", "recovered_terminal", "admission_failure"} {
		t.Run(source, func(t *testing.T) {
			s, db, root := reliableAgentRoot(t)
			finishConversationTestRun(t, db, root.ID)
			for _, key := range []string{"explicit-first", "explicit-second"} {
				input := queueTestInput(root.ID, key, "interject")
				raw, _ := json.Marshal(input.Request)
				if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
					return tx.InsertAgentQueuedMessage(&model.AgentQueuedMessage{ID: key, UserID: "user", CanvasID: "agent-canvas", ConversationID: root.ID, IdempotencyKey: key, Fingerprint: key, RequestJSON: string(raw), Prompt: input.Request.Prompt, Mode: "interject"}, q, 20, 100)
				}); err != nil {
					t.Fatal(err)
				}
			}
			switch source {
			case "observed_terminal":
				current, err := s.repo.CloudAgent("user", root.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.repo.MutateCloudAgent("user", root.ID, current.Revision, func(run *model.CloudAgentExecution, _ *repository.Repository) error {
					run.Status = "failed"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "recovered_terminal":
				// Simulate a persisted old checkpoint being reconciled on restart.
				if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("status", "failed").Error; err != nil {
					t.Fatal(err)
				}
			case "admission_failure":
				var claimed model.AgentQueuedMessage
				if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, _ *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
					row, err := tx.AgentQueuedMessage("user", root.ID, "explicit-first")
					if err != nil {
						return err
					}
					if err = tx.ClaimAgentQueuedMessage(row, "failed-first", latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
						return err
					}
					claimed = *row
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.recoverAgentMessageClaim(claimed, repository.ErrInsufficientCredits); err != nil {
					t.Fatal(err)
				}
			}
			other := &Service{repo: repository.New(db), dataDir: s.dataDir}
			if n, err := other.DispatchAgentMessages(1); err != nil || n != 0 {
				t.Fatalf("earlier explicit intent bypassed a new failure: %d %v", n, err)
			}
			view, err := other.AgentMessageQueue("user", root.ID)
			if err != nil || !view.Paused || len(view.Items) != 2 {
				t.Fatalf("failure lost pending messages: %+v %v", view, err)
			}
		})
	}
}

func TestAgentMessageQueueExpiredPreparationCannotOvertakePromotion(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	var first, promoted string
	for _, key := range []string{"expired-preparation", "later-promotion"} {
		receipt, err := s.SendAgentConversationMessage("user", root.ID, queueTestInput(root.ID, key, "queue"))
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = receipt.MessageID
		} else {
			promoted = receipt.MessageID
		}
	}
	finishConversationTestRun(t, db, root.ID)
	var message model.AgentQueuedMessage
	if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, _ *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage("user", root.ID, first)
		if err != nil {
			return err
		}
		if err = tx.ClaimAgentQueuedMessage(row, "old-preparation", latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
			return err
		}
		message = *row
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var request CloudAgentRequest
	if err := json.Unmarshal([]byte(message.RequestJSON), &request); err != nil {
		t.Fatal(err)
	}
	var admission repository.CloudAgentAdmission
	if _, err := s.createCloudAgentRunScoped("user", request, root.ID, &cloudAgentRunScope{Prepared: &admission}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentQueuedMessage{}).Where("id = ?", first).Update("lease_until", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.WithAgentConversation("user", root.ID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage("user", root.ID, promoted)
		if err != nil {
			return err
		}
		if err = tx.PromoteAgentQueuedMessage(row); err != nil {
			return err
		}
		row, err = tx.AgentQueuedMessageForDispatch("user", root.ID)
		if err != nil {
			return err
		}
		if row == nil || row.ID != promoted {
			t.Fatalf("promotion was not selected: %+v", row)
		}
		if err = tx.ClaimAgentQueuedMessage(row, "new-owner", latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
			return err
		}
		return tx.SaveAgentConversationQueue(q)
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.repo.CreditAccount("user")
	policy, _ := s.RuntimePolicy()
	if err := s.repo.CommitAgentMessageAdmission(message, admission, policy.Task.ActiveTaskLimit, policy.Resource.TaskCount, gigabytes(policy.Resource.TaskDataGB)); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("expired preparation overtook the new owner: %v", err)
	}
	after, _ := s.repo.CreditAccount("user")
	if after.ReservedMicrocredits != before.ReservedMicrocredits {
		t.Fatalf("expired admission reserved credits: %+v => %+v", before, after)
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
