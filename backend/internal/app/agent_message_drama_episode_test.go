package app

import (
	"errors"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAgentMessageDramaEpisodeDelivery(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	for _, row := range []any{
		&model.Project{ID: "drama-project", UserID: "user", Name: "Drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "drama-project", UserID: "user", Status: "paused", Stage: "script"},
		&model.DramaEpisode{ID: "episode-1", ProjectID: "drama-project", Number: 1},
		&model.DramaEpisode{ID: "episode-2", ProjectID: "drama-project", Number: 2},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := agentTestRequest()
	request.Scope = &AgentBusinessScope{Type: "drama", ID: "drama-project"}
	request.CanvasID, request.ContextScope, request.DramaEpisodeID = "", nil, "episode-1"
	root, err := s.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	makeInput := func(key, episode string) AgentConversationMessageRequest {
		input := AgentConversationMessageRequest{Mode: "interject", ExpectedRunID: root.ID, Request: request}
		input.Request.IdempotencyKey, input.Request.Prompt, input.Request.DramaEpisodeID = key, key, episode
		return input
	}
	same, err := s.SendAgentConversationMessage("user", root.ID, makeInput("same-episode-message", "episode-1"))
	if err != nil || same.Delivery != "interjected" {
		t.Fatalf("same-episode message did not interject: %+v %v", same, err)
	}
	changed := makeInput("change-episode-message", "episode-2")
	changed.InheritRunConfiguration = true
	queued, err := s.SendAgentConversationMessage("user", root.ID, changed)
	if err != nil || queued.Delivery != "queued" {
		t.Fatalf("changed episode did not queue: %+v %v", queued, err)
	}
	queue, err := s.AgentMessageQueue("user", root.ID)
	if err != nil || len(queue.Items) != 1 {
		t.Fatalf("missing queued episode change: %+v %v", queue, err)
	}
	if _, err := s.UpdateAgentQueuedMessage("user", root.ID, queued.MessageID, AgentQueuedMessageUpdate{Revision: queue.Items[0].Revision, Action: "interject"}); err == nil {
		t.Fatal("promotion dropped the episode change")
	}
	finishConversationTestRun(t, db, root.ID)
	if count, err := s.DispatchAgentMessages(10); err != nil || count != 1 {
		t.Fatalf("episode change did not dispatch: %d %v", count, err)
	}
	queue, err = s.AgentMessageQueue("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.CloudAgentRun("user", queue.LatestRunID)
	if err != nil || next.DramaEpisodeID != "episode-2" {
		t.Fatalf("new run lost the queued episode: %+v %v", next, err)
	}

	run, state := agentInterjectionState(t, s, next.ID)
	state.event(next.ID, "user_question", map[string]any{"question": "Continue?"})
	if err := s.repo.MutateCloudAgent("user", next.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "completed"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	question, err := s.AgentMessageQueue("user", root.ID)
	if err != nil || question.AwaitingQuestionID == "" {
		t.Fatalf("missing question: %+v %v", question, err)
	}
	answer := makeInput("wrong-episode-answer", "episode-1")
	answer.ExpectedRunID, answer.ReplyToQuestionID = next.ID, question.AwaitingQuestionID
	_, err = s.SendAgentConversationMessage("user", root.ID, answer)
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || appErr.Reason != "agent_question_stale" {
		t.Fatalf("mismatched question reply was not rejected: %v", err)
	}
	answer.Request.DramaEpisodeID = "episode-2"
	if receipt, err := s.SendAgentConversationMessage("user", root.ID, answer); err != nil || receipt.Delivery != "started" {
		t.Fatalf("compatible answer could not use the same request key: %+v %v", receipt, err)
	}
}
